package core

import (
	"context"
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/clashapi"
	"github.com/veilon/veilon-app/veilond/internal/transport"
)

// autoSwitcher drives one hysteresis controller per selector group.
// It never persists anything: decisions live in memory and the
// core's cache_file only remembers the current selection.
type autoSwitcher struct {
	policy profile.Fallback
	ctrl   map[string]*transport.Controller // selector tag -> controller
}

func newAutoSwitcher(policy profile.Fallback) *autoSwitcher {
	return &autoSwitcher{policy: policy, ctrl: map[string]*transport.Controller{}}
}

// candidates returns the transports usable for a group on this node.
func (m *Manager) candidates(node *profile.Node, group string) []profile.TransportID {
	settings := m.store.LoadSettings()
	var out []profile.TransportID
	for _, id := range profile.AllTransports {
		tr := m.trans[id]
		if !tr.Configured(node) || settings.DisabledTransport[string(id)] {
			continue
		}
		if group == singbox.TagSelectorUDP && !id.SupportsUDP() {
			continue
		}
		out = append(out, id)
	}
	return out
}

// step evaluates both groups once and applies switches through the Clash
// API. It returns the decisions taken (for logs and tests).
func (a *autoSwitcher) step(ctx context.Context, m *Manager, clash *clashapi.Client, node *profile.Node, now time.Time) []transport.Decision {
	health := map[profile.TransportID]transport.Health{}
	for id, tr := range m.trans {
		health[id] = tr.Health()
	}
	var out []transport.Decision
	for _, sel := range []string{singbox.TagSelectorTCP, singbox.TagSelectorUDP} {
		g, err := clash.Group(ctx, sel)
		if err != nil {
			continue // UDP group absent when no UDP transport is enabled
		}
		cur := profile.TransportID(tagToID(g.Now))
		c := a.ctrl[sel]
		if c == nil || c.Current() != cur {
			// First run, or the selection changed underneath (core restart,
			// manual choice): restart the controller from the real state.
			c = transport.NewController(a.policy, cur, now)
			a.ctrl[sel] = c
		}
		d := c.Evaluate(now, health, m.candidates(node, sel))
		if !d.Switch {
			continue
		}
		if err := clash.Select(ctx, sel, singbox.TagFor(d.To)); err != nil {
			// Revert the controller so the next round retries from reality.
			delete(a.ctrl, sel)
			continue
		}
		m.opt.Logger.Info("auto-switch", "group", sel, "to", string(d.To), "reason", d.Reason)
		out = append(out, d)
	}
	return out
}
