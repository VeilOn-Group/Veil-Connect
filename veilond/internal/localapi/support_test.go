package localapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/sign"
	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/logring"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

// supAPI records support calls.
type supAPI struct {
	paths  []string
	bodies []string
}

func (*supAPI) Register(context.Context, ed25519.PrivateKey, string) (*apitypes.TokenResponse, error) {
	return nil, nil
}
func (*supAPI) Login(context.Context, string, ed25519.PrivateKey, string) (*apitypes.TokenResponse, error) {
	return nil, nil
}
func (*supAPI) Refresh(context.Context, string, ed25519.PrivateKey) (*apitypes.TokenResponse, error) {
	return nil, nil
}
func (*supAPI) Logout(context.Context, string, string) error            { return nil }
func (*supAPI) Keyset(context.Context) (*sign.Envelope, error)          { return nil, nil }
func (*supAPI) Profile(context.Context, string) (*sign.Envelope, error) { return nil, nil }
func (a *supAPI) SupportGet(_ context.Context, _, path string) (json.RawMessage, error) {
	a.paths = append(a.paths, "GET "+path)
	return json.RawMessage(`[]`), nil
}
func (a *supAPI) SupportPost(_ context.Context, _, path string, in any) (json.RawMessage, error) {
	b, _ := json.Marshal(in)
	a.paths = append(a.paths, "POST "+path)
	a.bodies = append(a.bodies, string(b))
	return json.RawMessage(`{"ok":true}`), nil
}

func TestSupportRelayAndDiagnostics(t *testing.T) {
	st, _ := state.Open(t.TempDir())
	_ = st.SaveTokens(state.Tokens{Access: "a", Refresh: "r", AccessExp: time.Now().Add(time.Hour).Unix()})
	ring, _ := logring.Open(t.TempDir(), 4096)
	_, _ = ring.Write([]byte("WARN dial 203.0.113.9:443 to blocked.example.org for me@mail.example: timeout\n"))
	api := &supAPI{}
	m := core.New(core.Options{ClientVersion: "0.6.1", RuntimeDir: t.TempDir()}, st, api)
	s, err := New(m, ring, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const tid = "7a1c0e52-1111-4a6b-9c11-000000000001"
	if w := do("GET", "/v1/support/tickets", ""); w.Code != 200 {
		t.Fatalf("list %d", w.Code)
	}
	if w := do("POST", "/v1/support/tickets", `{"subject":"s","body":"b","lang":"ru"}`); w.Code != 200 {
		t.Fatalf("open %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/v1/support/tickets/"+tid+"/human", ""); w.Code != 200 {
		t.Fatalf("human %d", w.Code)
	}
	if w := do("POST", "/v1/support/tickets", `{"subject":"s","evil":"x"}`); w.Code != 400 {
		t.Fatalf("unknown field %d", w.Code)
	}
	// Raw attachments cannot be relayed: only the previewed diagnostics.
	if w := do("POST", "/v1/support/tickets/"+tid+"/attachments", `{}`); w.Code != 404 {
		t.Fatalf("raw attachment %d", w.Code)
	}
	if w := do("POST", "/v1/support/tickets/../../billing/checkout", `{}`); w.Code == 200 {
		t.Fatal("path escape relayed")
	}
	if w := do("POST", "/v1/support/tickets/"+tid+"/diagnostics", `{"sha256":"00"}`); w.Code != 409 {
		t.Fatalf("attach without preview %d", w.Code)
	}
	w := do("GET", "/v1/support/diagnostics", "")
	var pv struct {
		SHA256 string          `json:"sha256"`
		Report json.RawMessage `json:"report"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &pv) != nil {
		t.Fatalf("preview %d", w.Code)
	}
	rep := string(pv.Report)
	for _, leak := range []string{"203.0.113.9", "blocked.example.org", "me@mail"} {
		if strings.Contains(rep, leak) {
			t.Fatalf("%q in diagnostics: %s", leak, rep)
		}
	}
	if !strings.Contains(rep, `"format":"veilon-diagnostics/1"`) || !strings.Contains(rep, `"version":"0.6.1"`) {
		t.Fatal(rep)
	}
	if w := do("POST", "/v1/support/tickets/"+tid+"/diagnostics", `{"sha256":"`+pv.SHA256+`"}`); w.Code != 200 {
		t.Fatalf("attach %d %s", w.Code, w.Body)
	}
	last := api.bodies[len(api.bodies)-1]
	if !bytes.Equal([]byte(last), pv.Report) || api.paths[len(api.paths)-1] != "POST /tickets/"+tid+"/attachments" {
		t.Fatalf("sent something else than previewed: %s", api.paths)
	}
	// The preview is single use.
	if w := do("POST", "/v1/support/tickets/"+tid+"/diagnostics", `{"sha256":"`+pv.SHA256+`"}`); w.Code != 409 {
		t.Fatalf("reuse %d", w.Code)
	}
	_ = http.StatusOK
}
