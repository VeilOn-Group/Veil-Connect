// Command veilon-updater is the privileged silent-update helper.
// It runs as root / LocalSystem, started by the OS service manager when
// veilond puts a request into the update inbox:
//
//	Linux    veilon-update.path (PathExists=<inbox>/request) -> veilon-update.service: veilon-updater apply
//	macOS    LaunchDaemon net.veilon.updater with WatchPaths <inbox>:   veilon-updater apply
//	Windows  service "VeilOnUpdater" polling the inbox:                 veilon-updater service
//
// Its only inputs are the root-owned veilond config (root keys, inbox and
// work paths) and its own build (version, platform). See
// internal/updater for the checks.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/veilon/veil-shared/pkg/release"
	"github.com/veilon/veilon-app/veilond/internal/config"
	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/trustpath"
	"github.com/veilon/veilon-app/veilond/internal/updater"
)

// Version is the version of the installed package (set at build time).
var Version = "0.1.0"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("veilon-updater", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", defaultConfig, "veilond config (root-owned)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := fs.Arg(0)
	if cmd == "" {
		cmd = "apply"
	}
	var err error
	switch cmd {
	case "apply":
		err = applyOnce(context.Background(), *cfgPath)
	case "version":
		fmt.Fprintln(stdout, "veilon-updater", Version, core.Platform())
	default:
		var handled bool
		handled, err = platformCommand(cmd, *cfgPath)
		if !handled {
			err = fmt.Errorf("unknown command %q (apply | version%s)", cmd, platformCommands)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "veilon-updater:", err)
		return 1
	}
	return 0
}

func load(cfgPath string) (*updater.Config, error) {
	if err := trustpath.CheckFile(cfgPath); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if cfg.Update.Inbox == "" || cfg.Update.WorkDir == "" {
		return nil, fmt.Errorf("config: update.inbox and update.work_dir are required")
	}
	// The work dir holds the verified copy: privileged owner, no other
	// access, trusted ancestors (no planted symlink or directory).
	if err := trustpath.EnsureDir(cfg.Update.WorkDir, trustpath.DirOptions{Perm: 0o700}); err != nil {
		return nil, err
	}
	if err := prepareInbox(cfg.Update.Inbox); err != nil {
		return nil, err
	}
	roots, err := cfg.RootPublicKeys()
	if err != nil {
		return nil, err
	}
	platform := core.Platform()
	inst, err := updater.ForFormat(release.FormatOf(platform))
	if err != nil {
		return nil, err
	}
	return &updater.Config{
		Inbox: cfg.Update.Inbox, WorkDir: cfg.Update.WorkDir, RootKeys: roots,
		Platform: platform, CurrentVersion: Version, Installer: inst,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}, nil
}

func applyOnce(ctx context.Context, cfgPath string) error {
	u, err := load(cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	_, err = u.ApplyOnce(ctx)
	return err
}
