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
