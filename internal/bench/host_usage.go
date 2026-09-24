package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// verifyHostUsage reconciles the main model's CLI total with proxy events.
// Codex auto-review calls are separate model usage and intentionally stay in
// the proxy total even though Codex's turn.completed omits them.
func verifyHostUsage(run RunRecord, agentLog string) error {
	_, err := matchHostSession(run, agentLog)
	return err
}

func matchHostSession(run RunRecord, agentLog string) (string, error) {
	if run.Agent != "claude" && run.Agent != "codex" {
		return "", nil
	}
	want, ok := run.ModelUsage[run.AgentModel]
	if !ok {
		return "", fmt.Errorf("host model usage missing")
	}
	got, err := reportedHostUsage(run.Agent, agentLog)
	if err != nil {
		return "", err
	}
	var matching []string
	for key, usage := range run.SessionUsage {
		if got.Input == usage.Input && got.Cached == usage.Cached && got.CacheWrite == usage.CacheWrite && got.Output == usage.Output {
			matching = append(matching, key)
		}
	}
	mainModelMatches := got.Input == want.Input && got.Cached == want.Cached && got.CacheWrite == want.CacheWrite && got.Output == want.Output
	if !mainModelMatches && len(matching) != 1 {
		return "", fmt.Errorf("host usage differs from proxy main model and sessions")
	}
	if len(matching) == 1 {
		return matching[0], nil
	}
	return "", nil
}

func reportedHostUsage(agent, agentLog string) (ModelUsage, error) {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return ModelUsage{}, err
	}
	var got ModelUsage
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct {
			Type  string `json:"type"`
			Usage struct {
				Input      *int `json:"input_tokens"`
				Output     *int `json:"output_tokens"`
				CacheRead  *int `json:"cache_read_input_tokens"`
				CacheWrite *int `json:"cache_creation_input_tokens"`
				Cached     *int `json:"cached_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Usage.Input == nil || event.Usage.Output == nil {
			continue
		}
		if (agent == "claude" && event.Type != "result") || (agent == "codex" && event.Type != "turn.completed") {
			continue
		}
		got.Input += *event.Usage.Input
		got.Output += *event.Usage.Output
		if agent == "claude" {
			if event.Usage.CacheRead == nil || event.Usage.CacheWrite == nil {
				return ModelUsage{}, fmt.Errorf("Claude CLI cache usage missing")
			}
			got.Cached += *event.Usage.CacheRead
			got.CacheWrite += *event.Usage.CacheWrite
		} else {
			if event.Usage.Cached == nil {
				return ModelUsage{}, fmt.Errorf("Codex CLI cache usage missing")
			}
			got.Cached += *event.Usage.Cached
		}
		found = true
	}
	if !found {
		return ModelUsage{}, fmt.Errorf("host usage missing")
	}
	return got, nil
}
