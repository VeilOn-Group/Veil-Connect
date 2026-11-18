package localapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRoutesWithoutAccount drives every route of a fresh client: no account,
// no profile and an API client without optional capabilities. Each error
// must map to its HTTP status, and local-only routes must work.
func TestRoutesWithoutAccount(t *testing.T) {
	s, _ := newServer(t)
	h := s.Handler()
	for _, c := range []struct {
		method, path, body string
		code               int
		has                string
	}{
		{"POST", "/v1/connect", "{", 400, "invalid body"},
		{"POST", "/v1/connect", "{}", 412, "no verified profile"},
		{"POST", "/v1/disconnect", "", 200, `"state":"disconnected"`},
		{"POST", "/v1/transport/select", "{", 400, "invalid body"},
		{"POST", "/v1/transport/select", `{"group":"x","transport":"vless"}`, 400, "group must be tcp or udp"},
		{"POST", "/v1/location", "{", 400, "invalid body"},
		{"POST", "/v1/location", `{"node_id":"x"}`, 412, "no verified profile"},
		{"GET", "/v1/account/export", "", 502, "not supported"},
		{"POST", "/v1/account/delete", "{", 400, "DELETE"},
		{"POST", "/v1/account/delete", `{"confirm":"DELETE"}`, 502, "not supported"},
		{"GET", "/v1/update", "", 502, "not supported"},
		{"POST", "/v1/update/download", "", 502, "check first"},
		{"POST", "/v1/update/install", "", 409, "silent install"},
		{"POST", "/v1/transport/enabled", "{", 400, "invalid body"},
		{"POST", "/v1/transport/enabled", `{"transport":"vless","enabled":false}`, 200, `"state"`},
		{"GET", "/v1/profile", "", 412, "no verified profile"},
		{"POST", "/v1/profile/refresh", "", 502, "not supported"},
		{"POST", "/v1/account/register", "", 502, "not supported"},
		{"POST", "/v1/account/login", "{", 400, "account_number"},
		{"POST", "/v1/account/login", `{"account_number":"1"}`, 502, "not supported"},
		{"POST", "/v1/account/logout", "", 200, `"ok":true`},
		{"GET", "/v1/account", "", 200, `"logged_in":false`},
		{"GET", "/v1/settings", "", 200, `"allow_lan"`},
		{"GET", "/v1/dns/presets", "", 200, `"default":"quad9"`},
		{"PUT", "/v1/settings", "{", 400, "invalid body"},
		{"PUT", "/v1/settings", `{"kill_switch":true}`, 400, "kill switch is not supported"},
		{"GET", "/v1/privacy/what-we-store", "", 200, `"stored"`},
		{"GET", "/v1/logs", "", 200, `"lines"`},
		{"POST", "/v1/logs/wipe", "", 200, `"ok":true`},
		{"GET", "/v1/billing/offers", "", 502, "not supported"},
		{"POST", "/v1/billing/checkout", "{", 400, "invalid body"},
		{"POST", "/v1/billing/checkout", `{"plan_id":"p","provider":"heleket"}`, 502, "not supported"},
		{"GET", "/v1/billing/order", "", 404, "no payment in progress"},
		{"GET", "/v1/billing/payment-url", "", 404, "no payment in progress"},
		{"GET", "/v1/support/tickets", "", 502, "not supported"},
		{"GET", "/v1/support/tickets/abc", "", 404, "unknown support request"},
		{"POST", "/v1/support/tickets", "{", 400, "invalid body"},
		{"POST", "/v1/support/tickets", `{"subject":"s","body":"b","lang":"en"}`, 502, "not supported"},
		{"POST", "/v1/support/tickets/abc/close", "", 404, "unknown support request"},
		{"POST", "/v1/support/tickets/abc/bogus", "", 404, "unknown support request"},
		{"GET", "/v1/support/diagnostics", "", 200, `"format":"veilon-diagn`},
		{"POST", "/v1/support/tickets/abc/diagnostics", "", 400, "sha256"},
		{"GET", "/v1/crash-reports", "", 200, `"auto_send":false`},
		{"POST", "/v1/crash-reports", "{", 400, "invalid crash report"},
		{"POST", "/v1/crash-reports", `{"component":"desktop","kind":"panic","text":"panic: x"}`, 400, "invalid crash report"},
		{"POST", "/v1/crash-reports/abc/send", "", 404, "no such report"},
		{"POST", "/v1/crash-reports/abc/delete", "", 404, "no such report"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, "http://veilond"+c.path, strings.NewReader(c.body)))
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.has) {
			t.Errorf("%s %s %s: %d %s (want %d, %q)", c.method, c.path, c.body, w.Code, strings.TrimSpace(w.Body.String()), c.code, c.has)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: responses must not be cached", c.method, c.path)
		}
	}
}
