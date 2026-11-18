package localapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
	"github.com/veilon/veilon-app/veilond/internal/logring"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

type crashAPI struct {
	supAPI
	sent []apitypes.CrashReport
}

func (a *crashAPI) SendCrashReport(_ context.Context, _ string, r apitypes.CrashReport) error {
	a.sent = append(a.sent, r)
	return nil
}

func TestCrashRoutes(t *testing.T) {
	st, _ := state.Open(t.TempDir())
	_ = st.SaveTokens(state.Tokens{Access: "a", Refresh: "r", AccessExp: time.Now().Add(time.Hour).Unix()})
	ring, _ := logring.Open(t.TempDir(), 4096)
	q, _ := crashq.Open(filepath.Join(t.TempDir(), "crash"), "0.8.0", nil)
	api := &crashAPI{}
	m := core.New(core.Options{ClientVersion: "0.8.0", RuntimeDir: t.TempDir(), Crashes: q}, st, api)
	s, err := New(m, ring, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	// The desktop UI submits its previous crash.
	ui := `{"kind":"panic","version":"0.8.0","text":"starting\npanic: window 192.0.2.7\n\ngoroutine 1 [running]:\nmain.main()\n\t/home/u/desktop/main.go:9 +0x2\n"}`
	if w := do("POST", "/v1/crash-reports", ui); w.Code != 204 {
		t.Fatalf("submit %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{
		`{"kind":"panic","version":"0.8.0","text":"no traceback here"}`,
		`{"kind":"exception","version":"0.8.0","text":"panic: x"}`,
		`{"kind":"panic","version":"0.8 beta","text":"panic: x"}`,
		`{"kind":"panic","version":"0.8.0","text":"panic: x","component":"veilond"}`,
	} {
		if w := do("POST", "/v1/crash-reports", bad); w.Code != 400 {
			t.Errorf("accepted %s: %d", bad, w.Code)
		}
	}
	w := do("GET", "/v1/crash-reports", "")
	var list struct {
		AutoSend bool `json:"auto_send"`
		Reports  []struct {
			ID, Component, Title, Text string
		} `json:"reports"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Reports) != 1 || list.AutoSend {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	r := list.Reports[0]
	if r.Component != "desktop" || r.Title != "panic: window [IP]" || strings.Contains(r.Text, "/home/u") {
		t.Fatalf("%+v", r)
	}
	if w := do("POST", "/v1/crash-reports/"+strings.Repeat("0", 32)+"/send", ""); w.Code != 404 {
		t.Fatalf("unknown id %d", w.Code)
	}
	if w := do("POST", "/v1/crash-reports/"+r.ID+"/send", ""); w.Code != 204 || len(api.sent) != 1 || api.sent[0].Text != r.Text {
		t.Fatalf("send %d %+v", w.Code, api.sent)
	}
	if w := do("POST", "/v1/crash-reports/"+r.ID+"/delete", ""); w.Code != 404 {
		t.Fatalf("sent report must be gone: %d", w.Code)
	}
}
