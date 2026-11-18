package core

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
)

// Crash reports (ADR-0077). veilond's own fatal errors and crashes of the
// cores are queued sanitized on the device. With the "crash_reports"
// setting on they are sent automatically; otherwise only when the user
// presses "Send" on a report. The server does not learn which account
// sent a report: the session only lets it rate-limit.

// CrashAPI is the crash part of the server API (apiclient.Client).
type CrashAPI interface {
	SendCrashReport(ctx context.Context, access string, r apitypes.CrashReport) error
}

var reVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,31}$`)

// ErrCrashReportsOff is returned when crash reporting is not available.
var ErrCrashReportsOff = errors.New("core: crash reporting is not available")

// crashHook returns the supervisor callback for a component.
func (m *Manager) crashHook(component string) func(kind, text string) {
	if m.opt.Crashes == nil {
		return nil
	}
	return func(kind, text string) {
		ver := ""
		if component == crash.ComponentSingBox {
			m.mu.Lock()
			ver = m.coreVer
			m.mu.Unlock()
		}
		m.RecordCrash(component, kind, ver, text)
	}
}

// RecordCrash queues a report (text is sanitized by the queue) and sends
// it right away if the user opted in.
func (m *Manager) RecordCrash(component, kind, componentVersion, text string) {
	q := m.opt.Crashes
	if q == nil {
		return
	}
	// The core reports its version through the Clash API ("1.13.21", but
	// builds may add text): keep it only when it is a plain version.
	if !reVersion.MatchString(componentVersion) {
		componentVersion = ""
	}
	if _, ok, err := q.Add(component, kind, componentVersion, text); err != nil || !ok {
		if err != nil {
			m.opt.Logger.Warn("crash report not queued", "component", component, "err", err)
		}
		return
	}
	m.opt.Logger.Info("crash report queued", "component", component, "kind", kind)
	if m.store.LoadSettings().CrashReports {
		m.flushCrashesAsync()
	}
}

// CrashReports lists the queued reports.
func (m *Manager) CrashReports() []apitypes.CrashReport {
	if m.opt.Crashes == nil {
		return nil
	}
	return m.opt.Crashes.List()
}

// DeleteCrashReport removes a queued report.
func (m *Manager) DeleteCrashReport(id string) error {
	if m.opt.Crashes == nil {
		return crashq.ErrNotFound
	}
	return m.opt.Crashes.Delete(id)
}

// SendCrashReport sends one report now (the user pressed "Send"); it is
// removed from the device once the server has it.
func (m *Manager) SendCrashReport(ctx context.Context, id string) error {
	if m.opt.Crashes == nil {
		return crashq.ErrNotFound
	}
	r, err := m.opt.Crashes.Get(id)
	if err != nil {
		return err
	}
	return m.sendCrash(ctx, r)
}

func (m *Manager) sendCrash(ctx context.Context, r apitypes.CrashReport) error {
	api, ok := m.api.(CrashAPI)
	if !ok {
		return ErrCrashReportsOff
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return err
	}
	err = api.SendCrashReport(ctx, access, r)
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// The server will never accept it (e.g. too old): drop it.
		_ = m.opt.Crashes.Delete(r.ID)
		return err
	}
	if err != nil {
		return err
	}
	return m.opt.Crashes.Delete(r.ID)
}

// FlushCrashReports sends every queued report if the user opted in.
// Reports stay queued on network errors and are retried at the next start,
// the next crash, or when the user turns sending on.
func (m *Manager) FlushCrashReports(ctx context.Context) (sent int, err error) {
	if m.opt.Crashes == nil || !m.store.LoadSettings().CrashReports {
		return 0, nil
	}
	for _, r := range m.opt.Crashes.List() {
		if err := m.sendCrash(ctx, r); err != nil {
			var apiErr *apiclient.APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
				continue
			}
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// flushCrashesAsync runs one flush in the background (never two at once).
func (m *Manager) flushCrashesAsync() {
	if m.opt.Crashes == nil || !m.crashFlushing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.crashFlushing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if n, err := m.FlushCrashReports(ctx); err != nil {
			m.opt.Logger.Info("crash reports not sent yet", "err", err)
		} else if n > 0 {
			m.opt.Logger.Info("crash reports sent", "count", n)
		}
	}()
}

// StartupCrashes queues veilond's own previous crash and sends pending
// reports if the user opted in. Called once at start.
func (m *Manager) StartupCrashes() {
	if m.opt.Crashes == nil {
		return
	}
	if queued, err := m.opt.Crashes.InstallSelf(); err != nil {
		m.opt.Logger.Warn("crash output not installed", "err", err)
	} else if queued {
		m.opt.Logger.Info("crash report queued", "component", crash.ComponentVeilond, "kind", crash.KindPanic)
	}
	m.flushCrashesAsync()
}

// AutoConnect connects at start when the user turned "connect at start"
// on (ADR-0076): refreshes the profile if none is cached, then connects to
// the last location. Errors are logged; the UI shows the state as usual.
// With the kill switch on, nothing leaves the host outside the tunnel from
// the moment the core is planned (ADR-0053).
func (m *Manager) AutoConnect(ctx context.Context) {
	if !m.store.LoadSettings().AutoConnectEnabled() {
		return
	}
	for attempt := 0; attempt < 6; attempt++ {
		m.mu.Lock()
		have := m.prof != nil
		m.mu.Unlock()
		if !have {
			if err := m.RefreshProfile(ctx); errors.Is(err, ErrNotLoggedIn) {
				m.opt.Logger.Info("auto-connect skipped: not signed in")
				return
			} else if err != nil {
				m.opt.Logger.Info("auto-connect: profile not ready", "err", err)
			}
		}
		err := m.Connect(ctx, "")
		if err == nil || errors.Is(err, ErrAlreadyRunning) {
			m.opt.Logger.Info("auto-connect started")
			return
		}
		if errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrNoKillSwitch) {
			m.opt.Logger.Info("auto-connect skipped", "err", err)
			return
		}
		m.opt.Logger.Info("auto-connect failed, retrying", "err", err)
		// At boot the network may come up after the service: back off.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(5<<attempt) * time.Second):
		}
	}
}
