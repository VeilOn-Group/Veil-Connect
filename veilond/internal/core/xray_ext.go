package core

import (
	"encoding/json"
	"path/filepath"

	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/xray"
	"github.com/veilon/veilon-app/veilond/internal/supervisor"
)

// xraySetup is what Connect needs to run the Xray process (ADR-0066).
type xraySetup struct {
	plan    *xray.Plan
	cfgPath string
	raw     []byte
}

// prepareXray plans the Xray process for the node's VLESS transports. With
// no Xray binary configured, XHTTP and Vision are switched off (sing-box
// alone cannot reach current nodes over VLESS).
func (m *Manager) prepareXray(node *profile.Node, disabled map[profile.TransportID]bool, res resolution, mark uint32) (*xraySetup, error) {
	if m.opt.XrayPath == "" {
		disabled[profile.TransportXHTTP], disabled[profile.TransportVLESS] = true, true
		return nil, nil //nolint:nilnil // no Xray: no process
	}
	plan, err := xray.PlanClient(node, xray.PlanOptions{
		Disabled: disabled, Mark: mark, ProcessPath: m.opt.XrayPath,
		Resolve: func(host string) string {
			if a := res.addrs[host]; len(a) > 0 {
				return a[0].String()
			}
			return ""
		},
	})
	if err != nil || plan == nil {
		return nil, err
	}
	doc, err := xray.BuildClient(plan.Spec)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return &xraySetup{plan: plan, cfgPath: filepath.Join(m.opt.RuntimeDir, "xray.json"), raw: raw}, nil
}

// xrayRunner builds the supervised Xray process (pinned like the core).
func (m *Manager) xrayRunner(xs *xraySetup) (Runner, error) {
	return m.opt.NewRunner(supervisor.Config{
		Bin: m.opt.XrayPath, ExpectedSHA256: m.opt.XraySHA256, AllowUnpinned: m.opt.AllowUnpinned,
		Args:        func() []string { return []string{"run", "-c", xs.cfgPath} },
		BeforeStart: func() error { return writeFile0600(xs.cfgPath, xs.raw) },
		Env:         []string{},
		Output:      m.opt.CoreOutput,
		Logger:      m.opt.Logger,
		OnCrash:     m.crashHook(crash.ComponentXray),
	})
}
