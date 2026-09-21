package proxy

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func evalCatalogBody(user string, names []string) []byte {
	tools := make([]any, 0, len(names))
	for _, n := range names {
		tools = append(tools, grokFn(n, n+" tool"))
	}
	raw, _ := json.Marshal(map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": user},
		},
		"tools": tools,
	})
	return raw
}

func catalogNames(body []byte) []string {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	var names []string
	for _, t := range asSlice(root["tools"]) {
		m, _ := t.(map[string]any)
		if n := toolNameOf(m); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func missedObserved(kept, observed []string) []string {
	var missed []string
	for _, name := range observed {
		if !containsName(kept, name) {
			missed = append(missed, name)
		}
	}
	return missed
}

func TestShadowLeavesOriginalCatalog(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := DefaultOptions()
	opt.Shadow = true
	out, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Shadow || stats.Apply != applyNone {
		t.Fatalf("shadow must not apply filter: %+v", stats)
	}
	if stats.ToolAfter != stats.ToolBefore || stats.ToolBefore != 6 {
		t.Fatalf("shadow must keep the original catalog: %+v", stats)
	}
	if len(stats.ProposedKept) == 0 || len(stats.ProposedKept) >= stats.ToolBefore {
		t.Fatalf("shadow must still compute a smaller proposed set: %+v", stats)
	}
	before, after := catalogNames(raw), catalogNames(out)
	if len(after) != len(before) {
		t.Fatalf("forwarded catalog changed: before=%v after=%v", before, after)
	}
	for _, name := range before {
		if !containsName(after, name) {
			t.Fatalf("forwarded catalog lost %s: %v", name, after)
		}
	}
}

func TestShadowRecordsObservedCoverage(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := DefaultOptions()
	opt.Shadow = true
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	hit := EventFromStats(stats)
	hit.ObservedTools = []string{"grep", "read_file"}
	markObservedCoverage(&hit)
	if hit.ShadowHit == nil || !*hit.ShadowHit || len(hit.Misexcluded) != 0 {
		t.Fatalf("observed locate pair must stay in proposed set: %+v proposed=%v", hit, stats.ProposedKept)
	}

	miss := EventFromStats(stats)
	miss.ObservedTools = []string{"run_terminal_cmd"}
	markObservedCoverage(&miss)
	if miss.ShadowHit == nil || *miss.ShadowHit || !containsName(miss.Misexcluded, "run_terminal_cmd") {
		t.Fatalf("unused tool must count as mis-exclusion: %+v", miss)
	}
}

func TestCostGateSkipsClassifierAtProductionThreshold(t *testing.T) {
	var calls int64
	client := fakeNextToolClient(t, "grep", 0.9, &calls)
	opt := DefaultOptions()
	opt.SelectionMode = SelectionJev
	opt.CostGateMax = decidedCostGateMaxCandidates

	three := evalCatalogBody("list files in this repo", []string{"read_file", "grep", "shell"})
	_, stats, err := RewriteWith(t.Context(), three, host.Grok, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCostGate || atomic.LoadInt64(&calls) != 0 {
		t.Fatalf("N<=3 must skip the classifier: calls=%d stats=%+v", calls, stats)
	}

	four := evalCatalogBody("list files in this repo", []string{"read_file", "grep", "shell", "web_search"})
	_, stats, err = RewriteWith(t.Context(), four, host.Grok, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&calls) == 0 || stats.Reason == reasonCostGate {
		t.Fatalf("N=4 must still ask: calls=%d stats=%+v", calls, stats)
	}
}

type shadowEvalCase struct {
	name     string
	host     host.ID
	user     string
	tools    []string
	observed []string
}

type shadowEvalRow struct {
	name          string
	n             int
	filterOK      bool
	shadowOK      bool
	filterAfter   int
	shadowAfter   int
	filterMiss    []string
	shadowMiss    []string
	filterElapsed time.Duration
	shadowElapsed time.Duration
	enableFilter  bool
}

func runShadowEval(t *testing.T, c shadowEvalCase) (filter, shadow shadowEvalRow) {
	t.Helper()
	raw := evalCatalogBody(c.user, c.tools)
	filterOpt := DefaultOptions()
	filterOpt.CostGateMax = 0
	shadowOpt := filterOpt
	shadowOpt.Shadow = true

	start := time.Now()
	_, fs, err := RewriteWith(t.Context(), raw, c.host, nil, filterOpt)
	if err != nil {
		t.Fatalf("%s filter: %v", c.name, err)
	}
	filterElapsed := time.Since(start)
	filterMiss := missedObserved(fs.ToolsAfter, c.observed)

	start = time.Now()
	_, ss, err := RewriteWith(t.Context(), raw, c.host, nil, shadowOpt)
	if err != nil {
		t.Fatalf("%s shadow: %v", c.name, err)
	}
	shadowElapsed := time.Since(start)
	kept := ss.ProposedKept
	if len(kept) == 0 {
		kept = ss.ToolsBefore
	}
	shadowMiss := missedObserved(kept, c.observed)

	n := len(c.tools)
	enable := n > decidedCostGateMaxCandidates && len(filterMiss) == 0
	base := shadowEvalRow{name: c.name, n: n}
	filter = base
	filter.filterOK = len(filterMiss) == 0
	filter.filterAfter = fs.ToolAfter
	filter.filterMiss = filterMiss
	filter.filterElapsed = filterElapsed
	filter.enableFilter = enable
	shadow = base
	shadow.shadowOK = len(shadowMiss) == 0
	shadow.shadowAfter = ss.ToolAfter
	shadow.shadowMiss = shadowMiss
	shadow.shadowElapsed = shadowElapsed
	shadow.enableFilter = enable
	return filter, shadow
}

func TestSideEffectFreeEvalDecidesFilterRange(t *testing.T) {
	const locate = "Do not parallel. Sequential search and read the definition of RewriteWith."
	cases := []shadowEvalCase{
		{
			name:     "codex_three",
			host:     host.Grok,
			user:     locate,
			tools:    []string{"read_file", "grep", "shell"},
			observed: []string{"grep", "read_file"},
		},
		{
			name:     "grok_six_locate",
			host:     host.Grok,
			user:     locate,
			tools:    []string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
			observed: []string{"grep", "read_file"},
		},
		{
			name:     "devin_six_locate",
			host:     host.Devin,
			user:     "ファイルを変更せず、RewriteWith の定義を検索して読んでください。",
			tools:    []string{"read", "grep", "exec", "edit", "run_subagent", "web_search"},
			observed: []string{"grep", "read"},
		},
	}

	var filterOK, shadowOK, enableN int
	var filterRemain, shadowRemain int
	for _, c := range cases {
		fr, sr := runShadowEval(t, c)
		if !sr.shadowOK {
			t.Fatalf("%s: no-filter/shadow must keep observed tools, missed=%v", c.name, sr.shadowMiss)
		}
		if fr.n <= decidedCostGateMaxCandidates && fr.enableFilter {
			t.Fatalf("%s: N<=3 must stay outside the enable range", c.name)
		}
		if fr.n > decidedCostGateMaxCandidates && !fr.filterOK {
			t.Fatalf("%s: N>3 locate must not mis-exclude observed tools: missed=%v", c.name, fr.filterMiss)
		}
		if fr.filterOK {
			filterOK++
		}
		if sr.shadowOK {
			shadowOK++
		}
		if fr.enableFilter {
			enableN++
		}
		filterRemain += fr.filterAfter
		shadowRemain += sr.shadowAfter
		t.Logf("%s n=%d filter_ok=%v miss=%v after=%d %s shadow_ok=%v after=%d %s enable=%v",
			c.name, fr.n, fr.filterOK, fr.filterMiss, fr.filterAfter, fr.filterElapsed,
			sr.shadowOK, sr.shadowAfter, sr.shadowElapsed, fr.enableFilter)
	}
	if filterOK != len(cases) || shadowOK != len(cases) {
		t.Fatalf("task success filter=%d/%d shadow=%d/%d", filterOK, len(cases), shadowOK, len(cases))
	}
	if enableN != 2 {
		t.Fatalf("enable filter only for N>3 locate hosts, got %d", enableN)
	}
	if filterRemain >= shadowRemain {
		t.Fatalf("filter must reduce remaining catalog cost: filter=%d shadow=%d", filterRemain, shadowRemain)
	}
}

func TestOptionsFromEnvPhaseDefaults(t *testing.T) {
	t.Setenv("JEV_ROUTING_MODE", "filter")
	t.Setenv("JEV_COMPACTION", "on")
	t.Setenv("JEV_REASONING", "legacy")
	t.Setenv("JEV_SELECTION_MODE", "hybrid")
	t.Setenv("JEV_COST_GATE_MAX", "")
	t.Setenv("JEV_SHADOW", "")
	t.Setenv("JEV_TRANSFORMS", "")

	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if o.CostGateMax != decidedCostGateMaxCandidates {
		t.Fatalf("production cost gate default=%d want %d", o.CostGateMax, decidedCostGateMaxCandidates)
	}
	if o.Shadow {
		t.Fatal("shadow defaults off")
	}
	if !o.Transforms.Compact || !o.Transforms.Filter || o.Transforms.Criteria {
		t.Fatalf("default transforms=%+v", o.Transforms)
	}

	t.Setenv("JEV_COST_GATE_MAX", "0")
	t.Setenv("JEV_SHADOW", "on")
	t.Setenv("JEV_TRANSFORMS", "compact=off,filter=on,criteria=off")
	o, err = OptionsFromEnv()
	if err != nil || o.CostGateMax != 0 || !o.Shadow || o.Transforms.Compact || !o.Transforms.Filter || o.Transforms.Criteria {
		t.Fatalf("overrides: %+v %v", o, err)
	}

	t.Setenv("JEV_COST_GATE_MAX", "-1")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("negative cost gate must fail")
	}
	t.Setenv("JEV_COST_GATE_MAX", "3")
	t.Setenv("JEV_SHADOW", "maybe")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("invalid shadow must fail")
	}
	t.Setenv("JEV_SHADOW", "off")
	t.Setenv("JEV_TRANSFORMS", "compact")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("invalid transforms must fail")
	}
}
