// Command veilond is the VeilOn client core service: it supervises sing-box,
// holds the verified profile and exposes the local control API to the UI.
// It runs with network privileges (Linux: a dedicated user with
// CAP_NET_ADMIN only; macOS: LaunchDaemon; Windows: LocalSystem service);
// the UI runs unprivileged.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/veilon/veil-shared/pkg/redact"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/config"
	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
	"github.com/veilon/veilon-app/veilond/internal/killswitch"
	"github.com/veilon/veilon-app/veilond/internal/localapi"
	"github.com/veilon/veilon-app/veilond/internal/logring"
	"github.com/veilon/veilon-app/veilond/internal/state"
	"github.com/veilon/veilon-app/veilond/internal/trustpath"
	"github.com/veilon/veilon-app/veilond/pkg/peercred"
)

// Version is set at build time: -ldflags "-X main.Version=x.y.z".
var Version = "0.1.0"

func main() {
	cfgPath := flag.String("config", envOr("VEILOND_CONFIG", "/etc/veilon/veilond.yaml"), "config file")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("veilond", Version)
		return
	}
	if handled, err := platformMain(*cfgPath, flag.Args()); handled || err != nil {
		if err != nil {
			fmt.Fprintln(os.Stderr, "veilond:", redact.New(redact.Options{Hostnames: true}).String(err.Error()))
			os.Exit(1)
		}
		return
	}
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "veilond:", redact.New(redact.Options{Hostnames: true}).String(err.Error()))
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(cfgPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runCtx(ctx, cfgPath)
}

// runCtx runs veilond until ctx ends (signals, or the Windows service
// control manager).
func runCtx(ctx context.Context, cfgPath string) error {
	// The config names the binaries veilond executes with privileges and
	// their pins: it must be as protected as the binaries.
	if err := trustpath.CheckFile(cfgPath); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	for _, d := range []string{cfg.StateDir, cfg.LogDir} {
		if err := trustpath.EnsureDir(d, trustpath.DirOptions{Perm: 0o700}); err != nil {
			return err
		}
	}
	// Runtime dir: traverse-only for others so the UI can open the verified
	// installer in runtime/update; everything else in it is 0600.
	if err := trustpath.EnsureDir(cfg.RuntimeDir, trustpath.DirOptions{Perm: 0o711}); err != nil {
		return err
	}
	policy, sockGID, err := apiPolicy(cfg)
	if err != nil {
		return err
	}
	ring, err := logring.Open(cfg.LogDir, logring.DefaultMaxBytes)
	if err != nil {
		return err
	}
	defer func() { _ = ring.Close() }()

	// Everything veilond and the core print goes through hostname-aware
	// redaction before touching disk. API hosts stay readable.
	scrub := redact.New(redact.Options{Hostnames: true, KeepHosts: hostsOf(cfg.API.BaseURLs)})
	log := redact.NewLogger(ring, slog.LevelInfo, scrub)
	coreOut := redact.NewWriter(ring, scrub)
	defer func() { _ = coreOut.Close() }()

	store, err := state.Open(cfg.StateDir)
	if err != nil {
		return err
	}
	roots, err := cfg.RootPublicKeys()
	if err != nil {
		return err
	}
	api, err := apiclient.New(apiclient.Options{
		BaseURLs: cfg.API.BaseURLs, SPKIPins: cfg.API.SPKIPins,
		AllowUnpinned: config.DevBuild && cfg.API.AllowUnpinned,
		Timeout:       time.Duration(cfg.API.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	// Crash reports: sanitized on the device, sent only with
	// the user's consent.
	crashes, err := crashq.Open(filepath.Join(cfg.StateDir, "crash"), Version, nil)
	if err != nil {
		return err
	}
	self, _ := os.Executable()
	var ks killswitch.Engine
	if !cfg.KillSwitch.Disabled {
		ks = killswitch.Default(cfg.StateDir)
	}
	m := core.New(core.Options{
		ClientVersion: Version,
		RuntimeDir:    cfg.RuntimeDir,
		SingBoxPath:   cfg.SingBox.Path, SingBoxSHA256: cfg.SingBox.SHA256,
		AllowUnpinned:  config.DevBuild && cfg.SingBox.AllowUnpinned,
		AllowInsecure:  config.DevBuild,
		TunStack:       cfg.TunStack,
		RootKeys:       roots,
		CoreOutput:     coreOut,
		Logger:         log,
		CoreExtraFiles: cfg.SingBox.ExtraFiles,
		NaiveMode:      cfg.Naive.Mode, NaivePath: cfg.Naive.Path, NaiveSHA256: cfg.Naive.SHA256,
		XrayPath: cfg.Xray.Path, XraySHA256: cfg.Xray.SHA256, PaymentHosts: cfg.Billing.PaymentHosts,
		KillSwitch:  ks,
		DaemonPath:  self,
		APIBaseURLs: cfg.API.BaseURLs,
		Crashes:     crashes,
	}, store, api)
	m.StartupCrashes()
	if err := m.LoadCachedProfile(); err != nil {
		log.Info("no cached profile yet", "err", err)
	}
	go m.RefreshLoop(ctx, time.Duration(cfg.ProfileRefreshMinutes)*time.Minute)
	srv, err := localapi.New(m, ring, log)
	if err != nil {
		return err
	}

	errc := make(chan error, 2)
	go func() {
		errc <- srv.ServeUnix(ctx, cfg.LocalAPI.Socket, localapi.UnixOptions{GID: sockGID, Policy: policy})
	}()
	if cfg.LocalAPI.TCPFallback != "" {
		go func() { errc <- srv.ServeTCP(ctx, cfg.LocalAPI.TCPFallback, cfg.LocalAPI.TokenFile) }()
	}
	log.Info("veilond started", "version", Version)
	go m.AutoConnect(ctx)

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			log.Error("local API stopped", "err", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.Disconnect(shutdownCtx)
	if m.Settings().ClearLogsOnExit {
		_ = ring.Wipe()
	}
	_ = os.Remove(cfg.LocalAPI.Socket)
	return nil
}

// apiPolicy builds the control-socket peer policy from the config and
// picks the socket group (unix): socket_gid, else the first allowed group.
func apiPolicy(cfg *config.Config) (peercred.Policy, int, error) {
	gids, sids, err := localapi.ResolveGroups(cfg.LocalAPI.AllowedGroups)
	if err != nil {
		return peercred.Policy{}, 0, err
	}
	p := peercred.Policy{GIDs: gids, SIDs: sids}
	sockGID := cfg.LocalAPI.SocketGID
	if sockGID > 0 {
		p.GIDs = append(p.GIDs, uint32(sockGID)) //nolint:gosec // validated >= 0
	} else if len(gids) > 0 {
		sockGID = int(gids[0])
	}
	for _, u := range cfg.LocalAPI.AllowedUIDs {
		p.UIDs = append(p.UIDs, uint32(u)) //nolint:gosec // validated >= 0
	}
	return p, sockGID, nil
}

func hostsOf(urls []string) []string {
	var out []string
	for _, u := range urls {
		h := u
		for _, p := range []string{"https://", "http://"} {
			if len(h) > len(p) && h[:len(p)] == p {
				h = h[len(p):]
			}
		}
		for i := 0; i < len(h); i++ {
			if h[i] == '/' || h[i] == ':' {
				h = h[:i]
				break
			}
		}
		out = append(out, h)
	}
	return out
}
