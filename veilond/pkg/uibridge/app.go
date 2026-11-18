package uibridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/veilon/veilon-app/veilond/pkg/autostart"
)

// AutostartPath is the UI-process route for the login item switch.
const AutostartPath = "/app/v1/autostart"

// AutostartHandler serves GET {"enabled","supported"} and PUT
// {"enabled": bool} for the login item of e.
func AutostartHandler(e autostart.Entry) http.Handler {
	type state struct {
		Enabled   bool `json:"enabled"`
		Supported bool `json:"supported"`
	}
	current := func() state {
		on, err := autostart.Enabled(e)
		return state{Enabled: on, Supported: err == nil}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut:
			var in struct {
				Enabled *bool `json:"enabled"`
			}
			dec := json.NewDecoder(io.LimitReader(r.Body, 1024))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil || in.Enabled == nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"bad_request","message":"enabled is required"}`))
				return
			}
			err := autostart.Disable(e)
			if *in.Enabled {
				err = autostart.Enable(e)
			}
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "autostart", "message": err.Error()})
				return
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(current())
	})
}

// InstallCrashOutput returns the traceback the previous UI run left in
// dir ("" if none), then makes the runtime write this run's fatal error
// there. dir is created 0700 in the user's cache directory.
func InstallCrashOutput(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "ui.trace")
	prev := ""
	if f, err := os.Open(p); err == nil { //nolint:gosec // fixed name in the user's cache dir
		b, _ := io.ReadAll(io.LimitReader(f, 128<<10))
		_ = f.Close()
		prev = string(b)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // fixed name
	if err != nil {
		return prev, err
	}
	// The runtime duplicates the descriptor: f can be closed here.
	err = debug.SetCrashOutput(f, debug.CrashOptions{})
	_ = f.Close()
	return prev, err
}

// SubmitCrash hands the UI's previous crash to veilond, which sanitizes,
// queues and (with consent) sends it. Called by the UI process, not the
// web view.
func (b *Bridge) SubmitCrash(ctx context.Context, version, text string) error {
	body, err := json.Marshal(map[string]string{"kind": "panic", "version": version, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://veilond/v1/crash-reports", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("uibridge: crash report: HTTP %d", resp.StatusCode)
	}
	return nil
}
