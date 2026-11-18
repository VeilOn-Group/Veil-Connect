package state

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/veilon/veil-shared/pkg/profile"
)

// ErrInvalidSettings marks user input errors (HTTP 400 in the local API).
var ErrInvalidSettings = errors.New("state: invalid settings")

// MaxSplitRules bounds the user's rule list.
const MaxSplitRules = 200

var (
	reDomain = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	reApp    = regexp.MustCompile(`^[A-Za-z0-9._ +-]{1,128}$`) // process name (desktop) or package name (Android)
)

// ValidateSplitRules checks every rule; values end up in the core config.
func ValidateSplitRules(rules []SplitRule) error {
	if len(rules) > MaxSplitRules {
		return fmt.Errorf("%w: at most %d split rules", ErrInvalidSettings, MaxSplitRules)
	}
	var errs []error
	for i, r := range rules {
		v := strings.TrimSpace(r.Value)
		switch r.Action {
		case "direct", "proxy", "block":
		default:
			errs = append(errs, fmt.Errorf("rule %d: action must be direct|proxy|block", i+1))
		}
		switch r.Kind {
		case "domain", "domain_suffix":
			if !reDomain.MatchString(strings.TrimPrefix(v, ".")) {
				errs = append(errs, fmt.Errorf("rule %d: invalid domain %q", i+1, v))
			}
		case "ip":
			if _, err := netip.ParsePrefix(v); err != nil {
				if a, err2 := netip.ParseAddr(v); err2 != nil || !a.IsValid() {
					errs = append(errs, fmt.Errorf("rule %d: invalid IP or CIDR %q", i+1, v))
				}
			}
		case "app":
			if !reApp.MatchString(v) {
				errs = append(errs, fmt.Errorf("rule %d: invalid application name %q", i+1, v))
			}
		default:
			errs = append(errs, fmt.Errorf("rule %d: kind must be domain|domain_suffix|ip|app", i+1))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidSettings, errors.Join(errs...))
	}
	return nil
}

// RouteRules converts validated split rules for the config generator.
func RouteRules(rules []SplitRule) []profile.RouteRule {
	out := make([]profile.RouteRule, 0, len(rules))
	for _, r := range rules {
		v := strings.TrimSpace(r.Value)
		rr := profile.RouteRule{Action: r.Action}
		switch r.Kind {
		case "domain":
			rr.Domain = []string{strings.ToLower(v)}
		case "domain_suffix":
			rr.DomainSuffix = []string{strings.ToLower(strings.TrimPrefix(v, "."))}
		case "ip":
			if _, err := netip.ParsePrefix(v); err != nil {
				a, _ := netip.ParseAddr(v)
				v = netip.PrefixFrom(a, a.BitLen()).String()
			}
			rr.IPCIDR = []string{v}
		case "app":
			// Desktop matches the process name; Android matches the package.
			if strings.Count(v, ".") >= 2 && !strings.HasSuffix(strings.ToLower(v), ".exe") {
				rr.PackageName = []string{v}
			} else {
				rr.ProcessName = []string{v}
			}
		}
		out = append(out, rr)
	}
	return out
}
