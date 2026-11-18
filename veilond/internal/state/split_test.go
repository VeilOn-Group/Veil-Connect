package state

import (
	"strings"
	"testing"
)

func TestSplitRules(t *testing.T) {
	ok := []SplitRule{
		{Kind: "domain_suffix", Value: ".Bank.Example.RS", Action: "direct"},
		{Kind: "ip", Value: "192.0.2.80", Action: "block"},
		{Kind: "ip", Value: "10.0.0.0/8", Action: "direct"},
		{Kind: "app", Value: "telegram.exe", Action: "proxy"},
		{Kind: "app", Value: "org.example.bank", Action: "direct"},
	}
	if err := ValidateSplitRules(ok); err != nil {
		t.Fatal(err)
	}
	rr := RouteRules(ok)
	if rr[0].DomainSuffix[0] != "bank.example.rs" || rr[1].IPCIDR[0] != "192.0.2.80/32" {
		t.Fatalf("conversion: %+v", rr)
	}
	if len(rr[3].ProcessName) != 1 || len(rr[4].PackageName) != 1 {
		t.Fatalf("app rules: %+v %+v", rr[3], rr[4])
	}
	for _, bad := range []SplitRule{
		{Kind: "domain", Value: "bad domain", Action: "direct"},
		{Kind: "ip", Value: "300.1.1.1", Action: "direct"},
		{Kind: "app", Value: "x\"; rm -rf", Action: "direct"},
		{Kind: "regex", Value: ".*", Action: "direct"},
		{Kind: "domain", Value: "example.com", Action: "allow"},
	} {
		if err := ValidateSplitRules([]SplitRule{bad}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	many := make([]SplitRule, MaxSplitRules+1)
	for i := range many {
		many[i] = SplitRule{Kind: "domain", Value: "a" + strings.Repeat("b", i%5) + ".example", Action: "direct"}
	}
	if err := ValidateSplitRules(many); err == nil {
		t.Error("too many rules accepted")
	}
}
