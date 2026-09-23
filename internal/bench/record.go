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
}
