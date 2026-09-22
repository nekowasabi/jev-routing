package proxy

import (
	"testing"
)

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

func TestClassMapAlwaysListsSixKinds(t *testing.T) {
	opt := DefaultOptions()
	cells := ClassMap(nil, nil, opt, "grok")
	if len(cells) != 7 {
		t.Fatalf("len=%d", len(cells))
	}
	got := map[string]string{}
	for _, c := range cells {
		got[c.Kind] = c.Status
		if c.Label == "" {
			t.Fatalf("empty label %+v", c)
		}
	}
	if got["model"] != ClassObserve || got["skill"] != ClassObserve || got["mcp_tool"] != ClassObserve || got["plugin"] != ClassObserve {
		t.Fatalf("default observe %+v", got)
	}
	if got["compaction"] != ClassUnobserved {
		t.Fatalf("compaction %+v", got)
	}
	opt.KindModes["skill"] = KindApply
	opt.KindModes["mcp_tool"] = KindApply
	opt.KindModes["plugin"] = KindApply
	opt.KindModes["model"] = KindApply
	opt.KindModes["cli"] = KindOff
	apply := ClassMap(nil, nil, opt, "grok")
	st := map[string]string{}
	for _, c := range apply {
		st[c.Kind] = c.Status
	}
	if st["skill"] != ClassUnobserved || st["mcp_tool"] != ClassUnobserved || st["plugin"] != ClassUnobserved {
		t.Fatalf("apply unused %+v", st)
	}
	if st["model"] != ClassUnsupported || st["cli"] != ClassOff {
		t.Fatalf("model/cli %+v", st)
	}
}

func TestClassMapRecordsSkillMCPPluginAndEffort(t *testing.T) {
	apps := []*Application{
		{Kind: "skill", State: AppDelivered, CapabilityID: "skill:host:review@1", Host: "claude"},
		{Kind: "mcp_tool", State: AppVerified, CapabilityID: "mcp_tool:host:lookup@1", Host: "grok", PluginOf: "plugin:test:devtools@1", Verified: true},
	}
	events := []Event{
		{Host: "codex", ReasoningChanged: true, OriginalModel: "gpt-x", SentModel: "gpt-x"},
		{Host: "devin", CompactApplied: true},
	}
	cells := ClassMap(apps, events, DefaultOptions(), "")
	st := map[string]ClassCell{}
	for _, c := range cells {
		st[c.Kind] = c
	}
	if st["skill"].Status != ClassDelivered || st["skill"].Count != 1 || st["skill"].Hosts["claude"] != 1 {
		t.Fatalf("skill %+v", st["skill"])
	}
	if st["mcp_tool"].Status != ClassVerified || st["mcp_tool"].Verified != 1 {
		t.Fatalf("mcp %+v", st["mcp_tool"])
	}
	if st["plugin"].Status != ClassVerified || st["plugin"].Count != 1 {
		t.Fatalf("plugin %+v", st["plugin"])
	}
	if st["model"].Status != ClassRewritten || st["model"].Evidence != "effort" {
		t.Fatalf("model %+v", st["model"])
	}
	if st["compaction"].Status != ClassRewritten {
		t.Fatalf("compaction %+v", st["compaction"])
	}
}
