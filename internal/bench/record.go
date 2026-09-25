package bench

// RunRecord is one agent session. Field names match jev-gateway-bench runs.jsonl
// so an existing summary can be rebuilt from either runner.
type RunRecord struct {
	Task                 string                  `json:"task"`
	Agent                string                  `json:"agent"`
	AgentModel           string                  `json:"agentModel,omitempty"`
	AgentEffort          string                  `json:"agentEffort,omitempty"`
	ApprovalMode         string                  `json:"approvalMode,omitempty"`
	CompareKey           string                  `json:"compareKey,omitempty"`
	EffectMinPairs       int                     `json:"effectMinPairs,omitempty"`
	EffectMinSavingsPct  float64                 `json:"effectMinSavingsPct,omitempty"`
	SourceRevision       string                  `json:"sourceRevision,omitempty"`
	UserTools            bool                    `json:"userTools"`
	NoHooks              bool                    `json:"noHooks,omitempty"`
	Catalog              int                     `json:"catalog,omitempty"`
	Mode                 string                  `json:"mode"`
	Rep                  int                     `json:"rep"`
	ExitCode             int                     `json:"exitCode"`
	TimedOut             bool                    `json:"timedOut"`
	Seconds              float64                 `json:"seconds"`
	Requests             int                     `json:"requests"`
	ControlRequests      int                     `json:"controlRequests,omitempty"`
	Metered              int                     `json:"metered"`
	MeterError           string                  `json:"meterError,omitempty"`
	UsageSource          string                  `json:"usageSource,omitempty"`
	TaskTokens           *int                    `json:"taskTokens,omitempty"`
	UnreportedUpstream   int                     `json:"unreportedUpstream"`
	CanceledUnreported   int                     `json:"canceledUnreported"`
	OnlyUsageMissing     bool                    `json:"onlyUsageMissing"`
	FailedRequests       int                     `json:"failedRequests"`
	Input                int                     `json:"input"`
	Cached               int                     `json:"cached"`
	CacheWrite           int                     `json:"cacheWrite"`
	Output               int                     `json:"output"`
	Reasoning            int                     `json:"reasoning"`
	LLMSeconds           float64                 `json:"llmSeconds"`
	JevCalls             int                     `json:"jevCalls"`
	JevApplied           int                     `json:"jevApplied"`
	EvidenceComplete     bool                    `json:"evidenceComplete"`
	JevInput             int                     `json:"jevInput"`
	JevOutput            int                     `json:"jevOutput"`
	JevSeconds           float64                 `json:"jevSeconds"`
	Modes                map[string]int          `json:"modes"`
	Models               []string                `json:"models,omitempty"`
	ModelUsage           map[string]ModelUsage   `json:"modelUsage,omitempty"`
	SessionUsage         map[string]SessionUsage `json:"sessionUsage,omitempty"`
	UnattributedRequests int                     `json:"unattributedRequests"`
	MainSessionKey       string                  `json:"mainSessionKey,omitempty"`
	ChildSessions        int                     `json:"childSessions"`
	ChildTokens          int                     `json:"childTokens"`
	ParentChildVerified  bool                    `json:"parentChildVerified"`
	AttributionMethod    string                  `json:"attributionMethod,omitempty"`
	SubagentModel        string                  `json:"subagentModel,omitempty"`
	ParentRequestSeqs    []int64                 `json:"parentRequestSeqs,omitempty"`
	ChildRequestSeqs     []int64                 `json:"childRequestSeqs,omitempty"`
	SubagentCalls        int                     `json:"subagentCalls"`
	HostUsageVerified    bool                    `json:"hostUsageVerified"`
	HostTranscript       *HostTranscriptUsage    `json:"hostTranscript,omitempty"`
	HostTranscriptError  string                  `json:"hostTranscriptError,omitempty"`
	Passed               int                     `json:"passed"`
	Total                int                     `json:"total"`
	Score                float64                 `json:"score"`
	Solved               bool                    `json:"solved"`
	Failed               []string                `json:"failed,omitempty"`
	VerifyTimedOut       bool                    `json:"verifyTimedOut,omitempty"`
	Isolation            *Isolation              `json:"isolation,omitempty"`
	Workspace            string                  `json:"workspace,omitempty"`

	CompactRequests           int `json:"compactRequests"`
	CompactRequested          int `json:"compactRequested,omitempty"`
	CompactDropped            int `json:"compactDropped"`
	CompactSavedTokens        int `json:"compactSavedTokens"`
	CodexCompactLimit         int `json:"codexCompactLimit,omitempty"`
	CodexCompactBaselineLimit int `json:"codexCompactBaselineLimit,omitempty"`
	// CodexToolOutputTruncateFlag is --codex-tool-output-truncate: true only
	// when this run is part of an explicit on-vs-off truncation comparison
	// (both "on" and "off" runs of that comparison set it, mirroring
	// ClaudeClear below). It is the jev_not_applied bypass signal in
	// comparison.go, not the run's actual truncation state.
	CodexToolOutputTruncateFlag bool `json:"codexToolOutputTruncateFlag,omitempty"`
	// CodexToolOutputTruncate is whether this specific run actually truncated
	// large Codex tool outputs -- on by default since internal/proxy
	// Options.CodexToolOutputTruncate defaults to true; see
	// JEV_CODEX_TOOL_OUTPUT_TRUNCATE. Always serialized (no omitempty) so a
	// run recorded before this default existed is distinguishable from one
	// where truncation was explicitly off.
	CodexToolOutputTruncate bool `json:"codexToolOutputTruncate"`
	// LargeFactsRefetches counts command_execution calls after the initial
	// full `cat` that touched the same logs/large-N.txt file again (e.g. a
	// narrower sed/rg re-read to recover a fact truncation cut from the
	// middle). Only populated for the large-facts task.
	LargeFactsRefetches int `json:"largeFactsRefetches,omitempty"`
	// CompactFactsRefetches is the same pattern as LargeFactsRefetches but
	// for compact-facts' logs/stage-N.txt files: how many times a file
	// already fully read was `cat`'d again, which only a legitimate
	// compaction should force. Unlike codexCompactEvidence (the strict
	// EvidenceComplete gate, which still rejects any duplicate read), this
	// counter is purely diagnostic and never affects EvidenceComplete.
	CompactFactsRefetches int `json:"compactFactsRefetches,omitempty"`

	// CodexNativeCompaction is this run's effective JEV_CODEX_NATIVE_COMPACTION
	// setting (opt.CodexNativeCompaction). CodexNativeCompactionFlag is
	// --codex-native-compaction: true only when this run is part of an
	// explicit on-vs-off native-compaction-replacement comparison (both
	// "off" and "on" runs of that comparison set it, mirroring
	// CodexToolOutputTruncateFlag above). computeCompareKey hashes a
	// constant (false) for CodexNativeCompaction on those runs instead of
	// the per-mode value, or the toggled axis under test would make every
	// off/on pair report settings_mismatch.
	CodexNativeCompaction     bool `json:"codexNativeCompaction,omitempty"`
	CodexNativeCompactionFlag bool `json:"codexNativeCompactionFlag,omitempty"`

	// CompactRequests (above) already counts the synthetic (native
	// replacement) route; CompactForwardedRequests is the same
	// NativeCompactionRequested candidates that instead went upstream
	// (fallback, or native replacement disabled for this run).
	CompactForwardedRequests int `json:"compactForwardedRequests,omitempty"`
	// CompactEvents is one entry per identified compaction request
	// (synthetic or forwarded) in request order, with the token cost paid
	// and the resulting summary size -- see gateway.go's meter loop.
	CompactEvents []CompactEventRecord `json:"compactEvents,omitempty"`
	// CompactApparentInput/Output is the sum, across this run's synthetic
	// compaction responses, of the apparent usage (see internal/proxy
	// native_compact.go compactUsage) reported to the CLI even though the
	// proxy never sent that request upstream. host_usage.go adds it back to
	// the proxy-side usage total before reconciling against the CLI's
	// reported total -- see docs/MEMO.md "計測上の教訓" and
	// HostUsageCompactCorrected below.
	CompactApparentInput  int `json:"compactApparentInput,omitempty"`
	CompactApparentOutput int `json:"compactApparentOutput,omitempty"`
	// HostUsageCompactCorrected/HostUsageCompactCorrectionInput/Output
	// record whether and how much of CompactApparentInput/Output was added
	// back to the proxy-side usage total before comparing it with the CLI's
	// reported total (see host_usage.go matchHostSession). This never
	// changes TaskTokens/UsageSource, which already exclude the apparent
	// usage by construction (gateway.go only counts a synthetic event's
	// Usage, which stays nil since it was never sent upstream).
	HostUsageCompactCorrected        bool `json:"hostUsageCompactCorrected,omitempty"`
	HostUsageCompactCorrectionInput  int  `json:"hostUsageCompactCorrectionInput,omitempty"`
	HostUsageCompactCorrectionOutput int  `json:"hostUsageCompactCorrectionOutput,omitempty"`
	// CompactPostRequests/CompactFirstPostInput/CompactFirstPostCached
	// describe what happened after this run's LAST identified compaction
	// request: how many upstream requests followed it, and the first one's
	// input tokens and cache-read tokens (a near-zero cache read would mean
	// the compaction destroyed the prompt cache prefix).
	CompactPostRequests    int `json:"compactPostRequests,omitempty"`
	CompactFirstPostInput  int `json:"compactFirstPostInput,omitempty"`
	CompactFirstPostCached int `json:"compactFirstPostCached,omitempty"`

	// ClaudeClear marks the Claude "on" condition as native context editing
	// (clear_tool_uses_20250919) rather than JEV_CLAUDE_ADVISE; see --claude-clear.
	ClaudeClear        bool `json:"claudeClear,omitempty"`
	ClaudeClearTrigger int  `json:"claudeClearTrigger,omitempty"`
	ClaudeClearAtLeast int  `json:"claudeClearAtLeast,omitempty"`
	ClaudeClearKeep    int  `json:"claudeClearKeep,omitempty"`
	// ClaudeClearExclude is the raw --claude-clear-exclude value (comma-
	// separated tool names); see internal/proxy Options.ClaudeClearExclude.
	ClaudeClearExclude string `json:"claudeClearExclude,omitempty"`
	// ClaudeClearGate is --claude-clear-gate when not "off" (see
	// internal/proxy Options.ClaudeClearGate).
	ClaudeClearGate    string `json:"claudeClearGate,omitempty"`
	ClearedToolUses    int    `json:"clearedToolUses,omitempty"`
	ClearedInputTokens int    `json:"clearedInputTokens,omitempty"`
	// ToolOutputTruncated/ToolOutputTruncatedBytes sum internal/proxy Event's
	// per-request toolOutputTruncated(Bytes) -- see --codex-tool-output-max.
	ToolOutputTruncated      int `json:"toolOutputTruncated,omitempty"`
	ToolOutputTruncatedBytes int `json:"toolOutputTruncatedBytes,omitempty"`

	// CodexSteerFlag is --codex-steer: true only when this run is part of an
	// explicit on-vs-off Codex tool-steering comparison (both "on" and "off"
	// runs of that comparison set it, mirroring CodexToolOutputTruncateFlag
	// above). It is also the jev_not_applied bypass signal in comparison.go.
	CodexSteerFlag bool `json:"codexSteerFlag,omitempty"`
	// CodexSteerForced/None count this run's codex-steer decisions by mode
	// (tool_choice forced to a specific tool, or forced to "none").
	// CodexSteerPassthrough breaks down the rest by passthrough reason.
	// Populated only when CodexSteerFlag is set -- see codex_steer.go.
	CodexSteerForced      int            `json:"codexSteerForced,omitempty"`
	CodexSteerNone        int            `json:"codexSteerNone,omitempty"`
	CodexSteerPassthrough map[string]int `json:"codexSteerPassthrough,omitempty"`

	// ClearNet* is the same-path net reduction native context editing gave
	// this "on" run against its own counterfactual (see clearnet.go), as
	// opposed to the on-vs-off run comparison the rest of this file reports.
	// Populated only for Claude runs with ClaudeClear set; nil means "not
	// measured" (wrong condition or missing artifacts), which is distinct
	// from a measured 0. ClearReworkTokens/ClearNetTokens/ClearNetPct stay
	// nil specifically when a run cleared tokens but its rework cost could
	// not be attributed (agent.log/proxy-events.json turn count mismatch) --
	// that run must not be silently treated as a zero-rework success.
	ClearSavedTokens     *int `json:"clearSavedTokens,omitempty"`
	ClearExtraCacheWrite *int `json:"clearExtraCacheWrite,omitempty"`
	ClearReworkTokens    *int `json:"clearReworkTokens,omitempty"`
	ClearNetTokens       *int `json:"clearNetTokens,omitempty"`
	// ClearNetPct is a fraction (0.145, not 14.5): net / cfTotal.
	ClearNetPct *float64 `json:"clearNetPct,omitempty"`
}

type ModelUsage struct {
	Requests   int `json:"requests"`
	Input      int `json:"input"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cacheWrite"`
	Output     int `json:"output"`
}

type SessionUsage struct {
	Requests   int `json:"requests"`
	Input      int `json:"input"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cacheWrite"`
	Output     int `json:"output"`
	JevCalls   int `json:"jevCalls"`
	JevInput   int `json:"jevInput"`
	JevOutput  int `json:"jevOutput"`
}

// CompactEventRecord is one native-compaction-identified request. For a
// "forwarded" route, InputTokens/OutputTokens/SummaryTokens are the real
// upstream usage (the cost was paying for the host's own summary). For a
// "synthetic" route, InputTokens/OutputTokens are the Jev cost of producing
// the retained transcript and SummaryTokens is only an estimate (summary
// bytes / 4, SummaryEstimated true) since no token count was ever reported
// for text the proxy never sent upstream.
type CompactEventRecord struct {
	Route            string `json:"route"` // "synthetic" | "forwarded"
	Reason           string `json:"reason,omitempty"`
	InputTokens      int    `json:"inputTokens"`
	OutputTokens     int    `json:"outputTokens"`
	SummaryTokens    int    `json:"summaryTokens"`
	SummaryEstimated bool   `json:"summaryEstimated,omitempty"`
}

type HostTranscriptUsage struct {
	PromptTokens     int      `json:"promptTokens"`
	CompletionTokens int      `json:"completionTokens"`
	CachedTokens     int      `json:"cachedTokens"`
	Steps            int      `json:"steps"`
	ModelNames       []string `json:"modelNames,omitempty"`
}

// usageSpec is how a provider's usage block adds up. The proxy copies the
// upstream fields as-is (proxy/usage.go extractUsage), so the meaning of
// Input, Cached and CacheWrite is the provider's own.
type usageSpec struct {
	label               string
	inputIncludesCached bool // OpenAI-style: cached_tokens is a subset of input
	hasCacheWrite       bool // Anthropic: cache_creation_input_tokens, billed apart
	reportsReasoning    bool // reasoning_tokens is reported (as part of output)
}

var usageSpecs = map[string]usageSpec{
	"claude": {"Anthropic Messages", false, true, false},
	"codex":  {"OpenAI Responses", true, false, true},
	"grok":   {"xAI", true, false, true},
}

// specOf falls back to OpenAI-style for devin and fake.
func specOf(agent string) usageSpec {
	if s, ok := usageSpecs[agent]; ok {
		return s
	}
	return usageSpec{"OpenAI-style", true, false, true}
}

// cacheWrite is the cache-write count the provider bills; 0 where it has none.
func cacheWrite(r RunRecord) int {
	if specOf(r.Agent).hasCacheWrite {
		return r.CacheWrite
	}
	return 0
}

// totalInput counts every input token the provider billed.
func totalInput(r RunRecord) int {
	total := r.Input + cacheWrite(r)
	if !specOf(r.Agent).inputIncludesCached {
		total += r.Cached
	}
	return total
}

// uncachedInput is the input billed at the full input price.
func uncachedInput(r RunRecord) int {
	return totalInput(r) - r.Cached - cacheWrite(r)
}
