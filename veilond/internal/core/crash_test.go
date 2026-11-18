package core

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
)

type crashFakeAPI struct {
	*fakeAPI
	mu     sync.Mutex
	got    []apitypes.CrashReport
	access []string
	status int // non-zero: fail with this HTTP status
}

func (f *crashFakeAPI) SendCrashReport(_ context.Context, access string, r apitypes.CrashReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		return &apiclient.APIError{Status: f.status}
	}
	f.got, f.access = append(f.got, r), append(f.access, access)
	return nil
}

func (f *crashFakeAPI) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

const coreTrace = "panic: dial 198.51.100.4 failed\n\ngoroutine 9 [running]:\ngithub.com/sagernet/sing-box/route.(*Router).Route(0xc0000a)\n\t/root/go/pkg/mod/github.com/sagernet/sing-box@v1.13.21/route/route.go:88 +0x1\n"

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCrashReportsConsent(t *testing.T) {
	base := newFakeAPI(t)
	api := &crashFakeAPI{fakeAPI: base}
	m, core, _ := newManager(t, base)
	m.api = api
	q, err := crashq.Open(filepath.Join(t.TempDir(), "crash"), "0.8.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	m.opt.Crashes = q
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}

	// Opt-in is off by default: the report waits on the device.
	if m.Settings().CrashReports {
		t.Fatal("crash reports must be off by default")
	}
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if core.cfg.OnCrash == nil {
		t.Fatal("the core supervisor must report crashes")
	}
	core.cfg.OnCrash("panic", coreTrace)
	reports := m.CrashReports()
	if len(reports) != 1 || reports[0].Component != "sing-box" || strings.Contains(reports[0].Text, "198.51.100.4") {
		t.Fatalf("%+v", reports)
	}
	time.Sleep(50 * time.Millisecond)
	if api.sent() != 0 {
		t.Fatal("sent without consent")
	}

	// Manual send: the user pressed "Send".
	if err := m.SendCrashReport(ctx, reports[0].ID); err != nil {
		t.Fatal(err)
	}
	if api.sent() != 1 || len(m.CrashReports()) != 0 {
		t.Fatal("manual send must deliver and dequeue")
	}

	// Queued while off, delivered when the user turns sending on.
	m.RecordCrash("veilond", "panic", "", coreTrace)
	s := m.Settings()
	s.CrashReports = true
	if err := m.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "flush on opt-in", func() bool { return api.sent() == 2 })

	// With consent, new crashes go out right away.
	m.RecordCrash("xray", "signal", "", "[signal SIGSEGV]\nlast line")
	waitFor(t, "send on crash", func() bool { return api.sent() == 3 })
	waitFor(t, "dequeue", func() bool { return len(m.CrashReports()) == 0 })

	// Refused by the server (400): dropped, never retried.
	api.mu.Lock()
	api.status = 400
	api.mu.Unlock()
	m.RecordCrash("veilond", "panic", "", coreTrace)
	waitFor(t, "drop on 400", func() bool { return len(m.CrashReports()) == 0 })

	// Network trouble (5xx): kept for later.
	api.mu.Lock()
	api.status = 503
	api.mu.Unlock()
	m.RecordCrash("veilond", "panic", "", coreTrace)
	time.Sleep(100 * time.Millisecond)
	if len(m.CrashReports()) != 1 {
		t.Fatal("report must stay queued on server errors")
	}
	for _, a := range api.access {
		if a == "" {
			t.Fatal("reports are sent with the session (rate limiting only)")
		}
	}
}

func TestAutoConnect(t *testing.T) {
	api := newFakeAPI(t)
	m, _, _ := newManager(t, api)
	ctx := context.Background()

	// Off by default: nothing happens.
	m.AutoConnect(ctx)
	if m.Status(ctx).State != Disconnected {
		t.Fatal("auto-connect must be opt-in")
	}
	// On but not signed in: skipped quickly, no retry loop.
	s := m.Settings()
	on := true
	s.AutoConnect = &on
	if err := m.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.AutoConnect(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("auto-connect without an account must give up at once")
	}
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	m.AutoConnect(ctx)
	if st := m.Status(ctx).State; st != Connected {
		t.Fatalf("state %s", st)
	}
	// An older UI that does not send auto_connect keeps the choice.
	s = m.Settings()
	s.AutoConnect = nil
	if err := m.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if !m.Settings().AutoConnectEnabled() {
		t.Fatal("auto_connect reset by an older UI")
	}
}
