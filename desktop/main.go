// Command veilon is the unprivileged desktop UI (Wails v3). It holds no
// secrets and never touches the network directly: everything goes to the
// privileged veilond service through pkg/uibridge (ADR-0006, ADR-0047).
//
// Windows: "main" (connect/profile/settings/privacy) and "support" (a second
// window, not a tab: spec 6.2). Built separately from the Go workspace
// because Wails v3 needs Go >= 1.26 and WebView headers (see Makefile/CI).
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/veilon/veilon-app/veilond/pkg/autostart"
	"github.com/veilon/veilon-app/veilond/pkg/uibridge"
)

// Version is set at build time: -ldflags "-X main.Version=x.y.z".
var Version = "0.1.0"

//go:embed all:frontend/dist
var assets embed.FS

// App icon generated from the brand master (tools/brand/gen.py, `make brand`).
//
//go:embed build/appicon.png
var appIcon []byte

// Theme background (#0A0A0F) so windows never flash white on open.
var bg = application.NewRGB(0x0A, 0x0A, 0x0F)

func socketPath() string {
	if v := os.Getenv("VEILOND_SOCKET"); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "windows":
		// AF_UNIX with a protected DACL and a peer-token check (ADR-0060).
		return `C:\ProgramData\VeilOn\sock\veilond.sock`
	case "darwin":
		return "/var/run/veilon/sock/veilond.sock"
	}
	return "/run/veilon/sock/veilond.sock"
}

// loginItem is the "start at login" entry (ADR-0076); it starts the UI
// minimised.
func loginItem() autostart.Entry {
	exe, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return autostart.Entry{ID: "net.veilon.app", Name: "VeilOn", Exe: exe, Args: []string{"--hidden"}}
}

func main() {
	// Not the flag package: the OS may pass its own arguments (e.g. macOS
	// -psn_*), which must not make the app exit.
	hidden := slices.Contains(os.Args[1:], "--hidden")
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	var app *application.App
	var bridge *uibridge.Bridge
	bridge = uibridge.New(socketPath(), uibridge.Actions{
		"open-support": func() { openSupport(app) },
		"open-payment": func() { openPayment(app, bridge) },
	})
	bridge.HandleApp(uibridge.AutostartPath, uibridge.AutostartHandler(loginItem()))
	go reportPreviousCrash(bridge)
	app = application.New(application.Options{
		Name:        "VeilOn",
		Description: "VeilOn VPN",
		Icon:        appIcon,
		Assets: application.AssetOptions{
			Handler:    application.BundledAssetFileServer(dist),
			Middleware: bridge.Middleware,
			// The AssetServer logs every request by default (verified in
			// application_options.go): disabled for zero-log hygiene.
			DisableLogging: true,
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	start := application.WindowStateNormal
	if hidden {
		start = application.WindowStateMinimised
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "VeilOn", URL: "/",
		Width: 420, Height: 760, MinWidth: 380, MinHeight: 640,
		BackgroundColour: bg,
		StartState:       start,
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// openPayment opens the provider page of the payment in progress in the
// system browser. The address is read from veilond by this process, never
// taken from the web view, and veilond only hands out https pages on the
// provider hosts (core.PaymentURLAllowed, checked again here).
func openPayment(app *application.App, b *uibridge.Bridge) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var p struct {
		URL string `json:"url"`
	}
	if err := b.GetJSON(ctx, "/v1/billing/payment-url", &p); err != nil {
		return
	}
	if !uibridge.PaymentURLAllowed(p.URL) {
		return
	}
	_ = app.Browser.OpenURL(p.URL)
}

// openSupport shows the support window, creating it on first use.
func openSupport(app *application.App) {
	if w, ok := app.Window.GetByName("support"); ok {
		w.Show()
		w.Focus()
		return
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "support", Title: "VeilOn Support", URL: "/#/support",
		Width: 480, Height: 720, MinWidth: 400, MinHeight: 560,
		BackgroundColour: bg,
	}).Show()
}

// reportPreviousCrash hands a crash of the previous UI run to veilond
// (ADR-0077): veilond sanitizes and queues it, and sends it only with the
// user's consent. The trace file lives in the user's cache directory.
func reportPreviousCrash(b *uibridge.Bridge) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	prev, err := uibridge.InstallCrashOutput(filepath.Join(dir, "VeilOn"))
	if err != nil || prev == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = b.SubmitCrash(ctx, Version, prev)
}
