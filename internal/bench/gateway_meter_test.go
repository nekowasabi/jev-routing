package bench

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeterSeparatesInferenceAndRequiresCompleteUsage(t *testing.T) {
	cases := []struct {
		name, response, wantError                                       string
		requests, controls, metered, input, output, jevInput, jevOutput int
	}{
		{
			name:     "complete",
			response: `{"router":{"oldestSeq":1},"events":[{"reason":"not_llm_path","requestPath":"/control","usageMissing":"no_usage"},{"requestPath":"/v1/responses","usage":{"inputTokens":12,"outputTokens":3},"jevCalls":1,"jevAttempts":[{"inputTokens":5,"outputTokens":2}]}]}`,
			requests: 1, controls: 1, metered: 1, input: 12, output: 3, jevInput: 5, jevOutput: 2,
		},
		{
			name:     "locally completed request",
			response: `{"events":[{"usageMissing":"not_called","jevCalls":1,"jevAttempts":[{"inputTokens":5,"outputTokens":2}]}]}`,
			jevInput: 5, jevOutput: 2,
		},
		{
			name:      "partial upstream",
			response:  `{"events":[{"usage":{"inputTokens":12},"usagePartial":true}]}`,
			wantError: "upstream usage incomplete", requests: 1,
		},
		{
			// Jev's own usage is no longer part of the primary token metric
			// (see docs/MEMO.md "主指標から Jev を除外"), so a gap in it must
			// not fail the meter -- only upstream usage gaps do.
			name:     "missing Jev output does not block the meter",
			response: `{"events":[{"usage":{"inputTokens":12,"outputTokens":3},"jevCalls":1,"jevAttempts":[{"inputTokens":5}]}]}`,
			requests: 1, metered: 1, input: 12, output: 3, jevInput: 5,
		},
		{
			name:      "history truncated by event limit",
			response:  `{"router":{"oldestSeq":2},"historyTruncated":false,"events":[]}`,
			wantError: "history truncated",
		},
		{
			name:      "history truncated flag",
			response:  `{"router":{"oldestSeq":1},"historyTruncated":true,"events":[]}`,
			wantError: "history truncated",
		},
		{
			name:      "actual dashboard truncation flag",
			response:  `{"eventsTruncated":true,"events":[]}`,
			wantError: "history truncated",
		},
		{
			name:      "no inference request",
			response:  `{"events":[{"reason":"not_llm_path"}]}`,
			wantError: "no inference requests recorded", controls: 1,
		},
		{
			// An unattributed Jev HTTP call no longer fails the meter for the
			// same reason: it is a Jev-side accounting gap, not an upstream one.
			name:     "unattributed Jev capability call does not block the meter",
			response: `{"jevHTTP":2,"events":[{"usage":{"inputTokens":12,"outputTokens":3},"jevCalls":1,"jevAttempts":[{"purpose":"selection","inputTokens":5,"outputTokens":2}]}]}`,
			requests: 1, metered: 1, input: 12, output: 3, jevInput: 5, jevOutput: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.response))
			}))
			defer srv.Close()
			got, err := (&gateway{origin: srv.URL}).meter()
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("meter error = %v, want %q", err, tc.wantError)
			}
			if got.Requests != tc.requests || got.ControlRequests != tc.controls || got.Metered != tc.metered || got.Input != tc.input || got.Output != tc.output || got.JevInput != tc.jevInput || got.JevOutput != tc.jevOutput {
				t.Fatalf("meter = %+v", got)
			}
		})
	}
}

func TestMeterCountsJevApplicationAndCapabilityUsage(t *testing.T) {
	const response = `{"events":[{"source":"jev","apply":"advise","usage":{"inputTokens":12,"outputTokens":3},"jevCalls":1,"jevAttempts":[{"purpose":"selection","inputTokens":5,"outputTokens":2}]}],"applications":[{"source":"jev","kind":"skill","state":"delivered"},{"source":"local","kind":"mcp_tool","state":"result_received","callId":"call-1","hasResult":true},{"source":"local","kind":"mcp_tool","state":"result_received","callId":"call-2","hasResult":true}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
	defer srv.Close()
	got, err := (&gateway{origin: srv.URL}).meter()
	if err != nil || got.JevApplied != 2 || got.JevCalls != 1 || got.JevInput != 5 || got.JevOutput != 2 {
		t.Fatalf("meter=%+v error=%v", got, err)
	}
}

// TestMeterSumsClearedToolUses covers the --claude-clear bench condition:
// the run total must sum clearedToolUses/clearedInputTokens across events.
func TestMeterSumsClearedToolUses(t *testing.T) {
	const response = `{"events":[` +
		`{"usageMissing":"not_called","clearedToolUses":0,"clearedInputTokens":0},` +
		`{"usage":{"inputTokens":12,"outputTokens":3},"clearedToolUses":2,"clearedInputTokens":58},` +
		`{"usage":{"inputTokens":9,"outputTokens":1},"clearedToolUses":1,"clearedInputTokens":30}` +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
	defer srv.Close()
	got, err := (&gateway{origin: srv.URL}).meter()
	if err != nil {
		t.Fatal(err)
	}
	if got.ClearedToolUses != 3 || got.ClearedInputTokens != 88 {
		t.Fatalf("meter=%+v", got)
	}
}

func TestMeterRejectsMissingToolEvidence(t *testing.T) {
	for _, applications := range []string{
		`[{"kind":"mcp_tool","state":"started","callId":"call-1","hasResult":false}]`,
		`[{"kind":"mcp_tool","state":"result_received","hasResult":true}]`,
	} {
		response := `{"events":[{"usage":{"inputTokens":12,"outputTokens":3}}],"applications":` + applications + `}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
		_, err := (&gateway{origin: srv.URL}).meter()
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "tool call ID or result missing") {
			t.Fatalf("applications=%s error=%v", applications, err)
		}
	}
}

func TestMeterDualFactsRequiresTwoVerifiedDistinctCalls(t *testing.T) {
	base := `{"events":[{"usage":{"inputTokens":12,"outputTokens":3}}],"applications":`
	left := `{"kind":"mcp_tool","state":"verified","capabilityId":"mcp_tool:host:bench_left_fact@obs","callId":"left-1","hasResult":true}`
	for _, tc := range []struct {
		name, apps string
		want       bool
	}{
		{"complete", `[` + left + `,{"kind":"mcp_tool","state":"verified","capabilityId":"mcp_tool:host:bench_right_fact@obs","callId":"right-1","hasResult":true}]`, true},
		{"real Claude MCP names", `[{"kind":"mcp_tool","state":"verified","capabilityId":"mcp_tool:host:mcp__bench__bench_left_fact@obs","callId":"left-1","hasResult":true},{"kind":"mcp_tool","state":"verified","capabilityId":"mcp_tool:host:mcp__bench__bench_right_fact@obs","callId":"right-1","hasResult":true}]`, true},
		{"missing right", `[` + left + `]`, false},
		{"same call ID", `[` + left + `,{"kind":"mcp_tool","state":"verified","capabilityId":"mcp_tool:host:bench_right_fact@obs","callId":"left-1","hasResult":true}]`, false},
		{"right result unverified", `[` + left + `,{"kind":"mcp_tool","state":"result_received","capabilityId":"mcp_tool:host:bench_right_fact@obs","callId":"right-1","hasResult":true}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(base + tc.apps + `}`)) }))
			defer srv.Close()
			got, err := (&gateway{origin: srv.URL}).meter("dual-facts")
			if err != nil || got.EvidenceComplete != tc.want {
				t.Fatalf("evidence=%t want=%t error=%v", got.EvidenceComplete, tc.want, err)
			}
		})
	}
}

func TestMeterSeparatesAutoReviewUsage(t *testing.T) {
	response := `{"jevHTTP":0,"events":[{"sentModel":"gpt-5.6-terra","usage":{"inputTokens":100,"cachedTokens":50,"outputTokens":10}},{"sentModel":"codex-auto-review","usage":{"inputTokens":20,"cachedTokens":0,"outputTokens":2}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
	defer srv.Close()
	got, err := (&gateway{origin: srv.URL}).meter()
	if err != nil || got.Input != 120 || got.Output != 12 || got.ModelUsage["gpt-5.6-terra"].Requests != 1 || got.ModelUsage["codex-auto-review"].Input != 20 {
		t.Fatalf("model usage=%+v err=%v", got, err)
	}
}

func TestMeterSeparatesSameModelSessions(t *testing.T) {
	response := `{"jevHTTP":0,"events":[{"sessionKey":"parent","sentModel":"claude-sonnet-5","usage":{"inputTokens":3,"cachedTokens":10,"outputTokens":2}},{"sessionKey":"child","sentModel":"claude-sonnet-5","usage":{"inputTokens":5,"cachedTokens":20,"outputTokens":4}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
	defer srv.Close()
	got, err := (&gateway{origin: srv.URL}).meter()
	if err != nil || got.ModelUsage["claude-sonnet-5"].Requests != 2 || got.SessionUsage["parent"].Input != 3 || got.SessionUsage["child"].Input != 5 || got.UnattributedRequests != 0 {
		t.Fatalf("session usage=%+v err=%v", got, err)
	}
}

func TestMeterChildFactsRequiresVerifiedAgentResult(t *testing.T) {
	base := `{"events":[{"sessionKey":"parent","usage":{"inputTokens":3,"outputTokens":2}},{"sessionKey":"child","usage":{"inputTokens":5,"outputTokens":4}}],"applications":`
	for _, tc := range []struct {
		app  string
		want bool
	}{
		{`[{"kind":"subagent","capabilityId":"subagent:host:Agent@obs","state":"verified","callId":"toolu_agent","hasResult":true}]`, true},
		{`[{"kind":"subagent","capabilityId":"subagent:host:Agent@obs","state":"selected","callId":"toolu_agent","hasResult":false}]`, false},
		{`[{"kind":"subagent","capabilityId":"subagent:host:read_subagent@obs","state":"verified","callId":"toolu_read","hasResult":true}]`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(base + tc.app + `}`)) }))
		got, err := (&gateway{origin: srv.URL}).meter("child-facts")
		srv.Close()
		if err != nil || got.EvidenceComplete != tc.want {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
}

func TestMeterKeepsMissingUsageScopeForHostAggregate(t *testing.T) {
	for _, tc := range []struct {
		name, response    string
		missing, canceled int
	}{
		{"grok canceled", `{"jevHTTP":0,"events":[{"sentModel":"grok-4.7","usage":{"inputTokens":10,"outputTokens":2}},{"sentModel":"grok-4.7","upstreamFinish":"canceled","canceled":true,"usage":null}]}`, 1, 1},
		{"devin connect", `{"jevHTTP":0,"events":[{"sentModel":"gpt-5-6-terra-medium","usageMissing":"no_usage"},{"sentModel":"gpt-5-6-terra-medium","usageMissing":"no_usage"}]}`, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.response)) }))
			defer srv.Close()
			got, err := (&gateway{origin: srv.URL}).meter()
			if err == nil || !got.OnlyUsageMissing || got.UnreportedUpstream != tc.missing || got.CanceledUnreported != tc.canceled {
				t.Fatalf("meter=%+v err=%v", got, err)
			}
		})
	}
}

// TestMeterRecordsCompactRouteAndReconciliation covers docs/MEMO.md
// "圧縮の同一経路指標": a synthesized compaction reply must be tallied separately
// from a forwarded one, its apparent usage summed for the CLI/proxy
// reconciliation correction, and never folded into the run's real Input/
// Output totals (it was never sent upstream).
func TestMeterRecordsCompactRouteAndReconciliation(t *testing.T) {
	const response = `{"events":[` +
		`{"usageMissing":"not_called","nativeCompactionRequested":true,"compactRoute":"synthetic",` +
		`"compactApparentInput":1,"compactApparentOutput":42,"compactSummaryBytes":180,` +
		`"jevCalls":1,"jevAttempts":[{"inputTokens":30,"outputTokens":12}]},` +
		`{"usage":{"inputTokens":900,"outputTokens":60},"nativeCompactionRequested":true,` +
		`"compactRoute":"forwarded","compactForwardReason":"native_compaction_off"},` +
		`{"usage":{"inputTokens":500,"outputTokens":20,"cachedTokens":400}}` +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
	defer srv.Close()
	got, err := (&gateway{origin: srv.URL}).meter()
	if err != nil {
		t.Fatal(err)
	}
	if got.CompactApparentInput != 1 || got.CompactApparentOutput != 42 {
		t.Fatalf("apparent usage = %d/%d, want 1/42", got.CompactApparentInput, got.CompactApparentOutput)
	}
	if got.CompactForwardedRequests != 1 {
		t.Fatalf("forwarded requests = %d, want 1", got.CompactForwardedRequests)
	}
	if len(got.CompactEvents) != 2 {
		t.Fatalf("compact events = %+v, want 2", got.CompactEvents)
	}
	synthetic, forwarded := got.CompactEvents[0], got.CompactEvents[1]
	if synthetic.Route != "synthetic" || synthetic.InputTokens != 30 || synthetic.OutputTokens != 12 || synthetic.SummaryTokens != 45 || !synthetic.SummaryEstimated {
		t.Fatalf("synthetic event = %+v", synthetic)
	}
	if forwarded.Route != "forwarded" || forwarded.Reason != "native_compaction_off" || forwarded.InputTokens != 900 || forwarded.OutputTokens != 60 || forwarded.SummaryTokens != 60 || forwarded.SummaryEstimated {
		t.Fatalf("forwarded event = %+v", forwarded)
	}
	if got.CompactPostRequests != 1 || got.CompactFirstPostInput != 500 || got.CompactFirstPostCached != 400 {
		t.Fatalf("post-compaction stats: requests=%d input=%d cached=%d", got.CompactPostRequests, got.CompactFirstPostInput, got.CompactFirstPostCached)
	}
	// The synthetic reply's apparent usage (input=1, output=42) must never
	// reach the run's real Input/Output totals: it was never sent upstream.
	if got.Input != 900+500 || got.Output != 60+20 {
		t.Fatalf("real usage totals leaked apparent usage: input=%d output=%d", got.Input, got.Output)
	}
}
