package localapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/veilon/veil-shared/pkg/redact"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/core"
)

// Support relay and diagnostics. The diagnostics report is
// built here, redacted (no IPs, host names, e-mail, keys), shown to the
// user, and only the exact previewed report can be attached: the UI sends
// back its SHA-256.

var diagScrubber = redact.New(redact.Options{Hostnames: true})

type diagCache struct {
	mu   sync.Mutex
	data []byte
	sum  string
}

func (s *Server) supportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/support/tickets", s.supportGet)
	mux.HandleFunc("GET /v1/support/tickets/{id}", s.supportGet)
	mux.HandleFunc("POST /v1/support/tickets", s.supportPost)
	mux.HandleFunc("POST /v1/support/tickets/{id}/{action}", s.supportPost)
	mux.HandleFunc("GET /v1/support/diagnostics", s.diagnosticsPreview)
	mux.HandleFunc("POST /v1/support/tickets/{id}/diagnostics", s.attachDiagnostics)
}

func (s *Server) supportFail(w http.ResponseWriter, err error) {
	var apiErr *apiclient.APIError
	switch {
	case errors.As(err, &apiErr):
		// Pass the API's own status and message through (404, 400, 429).
		writeErr(w, apiErr.Status, apiErr.Body.Code, apiErr.Body.Message)
	case errors.Is(err, core.ErrSupportPath):
		writeErr(w, 404, "not_found", err.Error())
	default:
		s.fail(w, err)
	}
}

func (s *Server) supportGet(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.SupportGet(r.Context(), strings.TrimPrefix(r.URL.Path, "/v1/support"))
	if err != nil {
		s.supportFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

func (s *Server) supportPost(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("action") == "attachments" || r.PathValue("action") == "diagnostics" {
		writeErr(w, 404, "not_found", "use /diagnostics")
		return
	}
	var body any
	raw, err := io.ReadAll(io.LimitReader(r.Body, 48<<10))
	if err != nil {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		var in map[string]string
		if err := json.Unmarshal(raw, &in); err != nil {
			writeErr(w, 400, "bad_request", "invalid body")
			return
		}
		for k := range in {
			if k != "subject" && k != "body" && k != "lang" {
				writeErr(w, 400, "bad_request", "unknown field "+k)
				return
			}
		}
		body = in
	}
	out, err := s.m.SupportPost(r.Context(), strings.TrimPrefix(r.URL.Path, "/v1/support"), body)
	if err != nil {
		s.supportFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

// diagnostics builds the report: versions, state, transport health,
// settings summary and the last log lines, all redacted.
func (s *Server) diagnostics(r *http.Request) ([]byte, error) {
	st := s.m.Status(r.Context())
	set := s.m.Settings()
	type tr struct {
		ID        string  `json:"id"`
		Enabled   bool    `json:"enabled"`
		Reachable bool    `json:"reachable"`
		LatencyMS int     `json:"latency_ms,omitempty"`
		Loss      float64 `json:"loss,omitempty"`
		LastError string  `json:"last_error,omitempty"`
	}
	var trs []tr
	for _, t := range st.Transports {
		x := tr{ID: string(t.ID), Enabled: t.Enabled, Reachable: t.Health.Reachable, LatencyMS: t.Health.LatencyMS, Loss: t.Health.Loss, LastError: t.Health.LastError}
		trs = append(trs, x)
	}
	lines, err := s.ring.Tail(80)
	if err != nil {
		lines = nil
	}
	rep := map[string]any{
		"format": "veilon-diagnostics/1",
		"client": map[string]any{"version": s.m.ClientVersion(), "os": runtime.GOOS, "arch": runtime.GOARCH, "core": st.CoreVersion},
		"state": map[string]any{"state": st.State, "error": st.Error, "node": st.NodeID, "active_tcp": st.ActiveTCP, "active_udp": st.ActiveUDP,
			"udp_available": st.UDPAvail, "kill_switch_active": st.KillSwitchActive, "has_profile": st.HasProfile},
		"transports": trs,
		"settings": map[string]any{"kill_switch": set.KillSwitch, "auto_switch": set.AutoSwitch, "bypass_ru": st.BypassRU,
			"allow_lan": set.AllowLAN, "split_rules": len(set.SplitRules), "disabled_transports": len(set.DisabledTransport)},
		"log": lines,
	}
	b, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	// Every string, keys included, goes through the scrubber.
	var v any
	_ = json.Unmarshal(b, &v)
	return json.Marshal(scrubJSON(v))
}

func scrubJSON(v any) any {
	switch t := v.(type) {
	case string:
		return diagScrubber.String(t)
	case []any:
		for i := range t {
			t[i] = scrubJSON(t[i])
		}
	case map[string]any:
		for k, x := range t {
			t[k] = scrubJSON(x)
		}
	}
	return v
}

func (s *Server) diagnosticsPreview(w http.ResponseWriter, r *http.Request) {
	b, err := s.diagnostics(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	sum := sha256.Sum256(b)
	s.diag.mu.Lock()
	s.diag.data, s.diag.sum = b, hex.EncodeToString(sum[:])
	s.diag.mu.Unlock()
	writeJSON(w, 200, map[string]any{"sha256": hex.EncodeToString(sum[:]), "report": json.RawMessage(b)})
}

func (s *Server) attachDiagnostics(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SHA256 string `json:"sha256"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "bad_request", "send the sha256 of the previewed report")
		return
	}
	s.diag.mu.Lock()
	data, sum := s.diag.data, s.diag.sum
	s.diag.mu.Unlock()
	if data == nil || in.SHA256 != sum {
		writeErr(w, 409, "conflict", "preview the diagnostics first")
		return
	}
	out, err := s.m.SupportPost(r.Context(), "/tickets/"+r.PathValue("id")+"/attachments", json.RawMessage(data))
	if err != nil {
		s.supportFail(w, err)
		return
	}
	s.diag.mu.Lock()
	s.diag.data, s.diag.sum = nil, ""
	s.diag.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
