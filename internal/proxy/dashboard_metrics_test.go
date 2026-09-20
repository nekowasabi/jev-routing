package proxy

import "testing"

func TestDashboardMetrics(t *testing.T) {
	apps := []*Application{
		{State: AppSelected, Kind: "cli"},
		{State: AppDelivered, Kind: "skill"},
		{State: AppVerified, Kind: "mcp_tool"},
		{State: AppFailed, Kind: "cli"},
		{State: AppUnknown, Kind: "plugin"},
	}
	got := MetricsFromApps(apps)
	if got.Selected < 1 || got.Delivered != 1 || got.Verified != 1 || got.Failed != 1 || got.Unknown != 1 || got.Unapplied < 3 {
		t.Fatalf("%+v", got)
	}
	if got.UnappliedReason["failed"] != 1 {
		t.Fatalf("reasons %+v", got.UnappliedReason)
	}
	events := []Event{
		{Source: "local", Changed: true, SelectionJevCalls: 0, Apply: "filter"},
		{Source: "jev", Changed: true, Apply: "none", Reason: "awaiting_approval", JevCalls: 1},
		{UsageMissing: "no_usage"},
	}
	ev := MetricsFromEvents(events)
	if ev.LocalSkip != 1 || ev.Unapplied != 1 || ev.MissingUsage != 1 {
		t.Fatalf("%+v", ev)
	}
	cur, base := 10, 20
	if EffectFromComparison(&cur, &base) != "decrease" {
		t.Fatal("decrease")
	}
	if EffectFromComparison(&base, &cur) != "increase" {
		t.Fatal("increase")
	}
	if EffectFromComparison(nil, &base) != "missing" {
		t.Fatal("missing")
	}
	merged := MergeMetrics(got, ev)
	if merged.LocalSkip != 1 || merged.Unapplied < 4 || merged.Verified != 1 {
		t.Fatalf("merge %+v", merged)
	}
}
