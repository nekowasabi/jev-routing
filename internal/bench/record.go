package bench

// RunRecord is one agent session. Field names match jev-gateway-bench runs.jsonl
// so an existing summary can be rebuilt from either runner.
type RunRecord struct {
	Task           string         `json:"task"`
	Agent          string         `json:"agent"`
	AgentModel     string         `json:"agentModel,omitempty"`
	UserTools      bool           `json:"userTools"`
	Mode           string         `json:"mode"`
	Rep            int            `json:"rep"`
	ExitCode       int            `json:"exitCode"`
	TimedOut       bool           `json:"timedOut"`
	Seconds        float64        `json:"seconds"`
	Requests       int            `json:"requests"`
	Metered        int            `json:"metered"`
	FailedRequests int            `json:"failedRequests"`
	Input          int            `json:"input"`
	Cached         int            `json:"cached"`
	CacheWrite     int            `json:"cacheWrite"`
	Output         int            `json:"output"`
	Reasoning      int            `json:"reasoning"`
	LLMSeconds     float64        `json:"llmSeconds"`
	JevCalls       int            `json:"jevCalls"`
	JevInput       int            `json:"jevInput"`
	JevSeconds     float64        `json:"jevSeconds"`
	Modes          map[string]int `json:"modes"`
	Models         []string       `json:"models,omitempty"`
	Passed         int            `json:"passed"`
	Total          int            `json:"total"`
	Score          float64        `json:"score"`
	Solved         bool           `json:"solved"`
	Failed         []string       `json:"failed,omitempty"`
	VerifyTimedOut bool           `json:"verifyTimedOut,omitempty"`
	Isolation      *Isolation     `json:"isolation,omitempty"`
	Workspace      string         `json:"workspace,omitempty"`

	CompactRequests    int `json:"compactRequests"`
	CompactDropped     int `json:"compactDropped"`
	CompactSavedTokens int `json:"compactSavedTokens"`
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
