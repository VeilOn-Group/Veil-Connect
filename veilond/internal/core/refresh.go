package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/url"
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
)

// Automatic pick-up of node configuration changes: the panel
// may change ports, keys or options of a node at any time. veilond
// refreshes the signed profile periodically and immediately when every
// transport of the active location fails; if the active location changed,
// it reconnects (the kill switch stays engaged).

const minRefreshGap = 2 * time.Minute

// nudgeRefresh asks the refresh loop for an early refresh (non-blocking).
func (m *Manager) nudgeRefresh() {
	select {
	case m.refreshCh <- struct{}{}:
	default:
	}
}

// RefreshLoop runs until ctx ends.
func (m *Manager) RefreshLoop(ctx context.Context, every time.Duration) {
	jitter := func() time.Duration { return every + time.Duration(rand.Int64N(int64(every)/5+1)) } //nolint:gosec // jitter, not security
	t := time.NewTimer(jitter())
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			t.Reset(jitter())
		case <-m.refreshCh:
			if time.Since(last) < minRefreshGap {
				continue
			}
		}
		last = time.Now()
		if _, err := m.store.LoadTokens(); err != nil {
			continue // not logged in
		}
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		changed, err := m.RefreshAndApply(rctx)
		cancel()
		switch {
		case err != nil:
			m.opt.Logger.Warn("profile refresh failed", "err", err)
		case changed:
			m.opt.Logger.Info("active location changed in the new profile: reconnected")
		}
	}
}

// RefreshAndApply refreshes the profile (through the tunnel, or, if that
// fails while connected, through the core's direct path) and reconnects
// when the active location's parameters changed.
func (m *Manager) RefreshAndApply(ctx context.Context) (bool, error) {
	m.mu.Lock()
	old, nodeID, sp := m.prof, m.activeNode, m.probe
	connected := m.state == Connected || m.state == Reconnecting
	m.mu.Unlock()
	err := m.RefreshProfile(ctx)
	if err != nil && connected && sp != nil {
		u := &url.URL{Scheme: "socks5", User: url.UserPassword("direct", sp.password), Host: sp.addr}
		err = m.RefreshProfile(apiclient.WithProxy(ctx, u))
	}
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	cur := m.prof
	m.mu.Unlock()
	if !connected || !nodeChanged(old, cur, nodeID) {
		return false, nil
	}
	if nodeOf(cur, nodeID) == nil {
		return false, errors.New("core: the active location is no longer in the profile")
	}
	return true, m.reconnectIfConnected(ctx)
}

func nodeOf(p *profile.Profile, id string) *profile.Node {
	if p == nil {
		return nil
	}
	for i := range p.Nodes {
		if p.Nodes[i].ID == id || (id == "" && i == 0) {
			return &p.Nodes[i]
		}
	}
	return nil
}

func nodeChanged(a, b *profile.Profile, id string) bool {
	na, nb := nodeOf(a, id), nodeOf(b, id)
	if na == nil || nb == nil {
		return na != nb
	}
	ja, _ := json.Marshal(na)
	jb, _ := json.Marshal(nb)
	return !bytes.Equal(ja, jb)
}
