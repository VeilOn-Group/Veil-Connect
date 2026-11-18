package transport

import (
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
)

// Controller decides transport switches with hysteresis. It is a
// pure state machine: the caller feeds health snapshots and applies decisions.
// Auto-switching is controlled by profile.Fallback.AutoSwitch.
type Controller struct {
	policy  profile.Fallback
	current profile.TransportID
	since   time.Time
	// streak counts consecutive evaluations in which the same candidate beat
	// the current transport by the margin.
	candidate profile.TransportID
	streak    int
	failures  int
}

// Decision is the controller output.
type Decision struct {
	Switch bool
	To     profile.TransportID
	Reason string
}

// NewController starts on transport cur at time now.
func NewController(policy profile.Fallback, cur profile.TransportID, now time.Time) *Controller {
	return &Controller{policy: policy, current: cur, since: now}
}

// Current returns the active transport.
func (c *Controller) Current() profile.TransportID { return c.current }

// score: lower is better. Loss and jitter add latency-equivalent penalties.
func score(h Health) float64 {
	if !h.Reachable {
		return 1e12
	}
	return float64(h.LatencyMS) + float64(h.JitterMS)*2 + h.Loss*1000
}

// Evaluate considers candidates (already filtered by the caller, e.g. only
// UDP-capable ones for the UDP group) and returns a decision.
func (c *Controller) Evaluate(now time.Time, health map[profile.TransportID]Health, candidates []profile.TransportID) Decision {
	cur := health[c.current]
	// 1) Hard failure of the current transport: switch immediately to the best
	//    reachable candidate after FailThreshold consecutive failures.
	if !cur.Reachable {
		c.failures++
	} else {
		c.failures = 0
	}
	best, bestScore := profile.TransportID(""), 1e12
	for _, t := range candidates {
		if t == c.current {
			continue
		}
		if s := score(health[t]); s < bestScore {
			best, bestScore = t, s
		}
	}
	if c.failures >= c.policy.FailThreshold && best != "" && bestScore < 1e12 {
		return c.switchTo(now, best, "current transport failed")
	}
	// 2) Soft improvement: margin, N consecutive wins, minimum dwell time.
	if best == "" || !cur.Reachable {
		c.candidate, c.streak = "", 0
		return Decision{}
	}
	curScore := score(cur)
	margin := float64(c.policy.SwitchMarginPct) / 100
	if bestScore < curScore*(1-margin) {
		if best == c.candidate {
			c.streak++
		} else {
			c.candidate, c.streak = best, 1
		}
	} else {
		c.candidate, c.streak = "", 0
	}
	dwell := now.Sub(c.since) >= time.Duration(c.policy.MinDwellSec)*time.Second
	if c.streak >= c.policy.ConsecutiveProbes && dwell {
		return c.switchTo(now, best, "candidate consistently better")
	}
	return Decision{}
}

func (c *Controller) switchTo(now time.Time, t profile.TransportID, why string) Decision {
	c.current, c.since = t, now
	c.candidate, c.streak, c.failures = "", 0, 0
	return Decision{Switch: true, To: t, Reason: why}
}
