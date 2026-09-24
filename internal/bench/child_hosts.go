package bench

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"strings"
)

// codexChildAttribution requires an independently metered child request. A
// paired tool result alone also records failed child launches as "verified".
func codexChildAttribution(agentLog string, snapshot []byte) (parent, child []int64, childTokens int, err error) {
	var body struct {
		Applications []struct {
			Kind         string `json:"kind"`
			State        string `json:"state"`
			CallID       string `json:"callId"`
			HasResult    bool   `json:"hasResult"`
			CapabilityID string `json:"capabilityId"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(snapshot, &body); err != nil {
		return nil, nil, 0, err
	}
	verified := 0
	for _, app := range body.Applications {
		if app.Kind != "subagent" || !isChildLaunch(app.CapabilityID) {
			continue
		}
		if app.State != "verified" || !app.HasResult || app.CallID == "" {
			return nil, nil, 0, fmt.Errorf("Codex child call lacks paired result")
		}
		verified++
	}
	if verified != 1 {
		return nil, nil, 0, fmt.Errorf("Codex child call count %d, want 1", verified)
	}
	parent, child, childTokens, err = partitionHostRequests("codex", agentLog, snapshot)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(child) == 0 || childTokens <= 0 {
		return nil, nil, 0, fmt.Errorf("Codex child has no independently metered request")
	}
	return parent, child, childTokens, nil
}

// grokChildAttribution matches the headless CLI's parent-only model usage to
// individual proxy requests. Grok's final JSON does not include child totals.
func grokChildAttribution(agentLog string, snapshot []byte) (parent, child []int64, childTokens int, err error) {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return nil, nil, 0, err
	}
	var host struct {
		UsageIncomplete bool `json:"usage_is_incomplete"`
		Usage           *struct {
			Input      *int `json:"input_tokens"`
			Output     *int `json:"output_tokens"`
			Cached     *int `json:"cache_read_input_tokens"`
			CacheWrite *int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			Input      *int `json:"inputTokens"`
			Output     *int `json:"outputTokens"`
			Cached     *int `json:"cacheReadInputTokens"`
			CacheWrite *int `json:"cacheCreationInputTokens"`
			Calls      *int `json:"modelCalls"`
		} `json:"modelUsage"`
	}
	if err := json.Unmarshal(raw, &host); err != nil {
		return nil, nil, 0, err
	}
	if host.UsageIncomplete || host.Usage == nil || host.Usage.Input == nil || host.Usage.Output == nil || host.Usage.Cached == nil || host.Usage.CacheWrite == nil || len(host.ModelUsage) == 0 {
		return nil, nil, 0, fmt.Errorf("Grok parent CLI usage incomplete")
	}
	want := map[string]ModelUsage{}
	var total ModelUsage
	for model, usage := range host.ModelUsage {
		if model == "" || usage.Input == nil || usage.Output == nil || usage.Cached == nil || usage.CacheWrite == nil || usage.Calls == nil || *usage.Calls <= 0 {
			return nil, nil, 0, fmt.Errorf("Grok parent model usage incomplete")
		}
		v := ModelUsage{Requests: *usage.Calls, Input: *usage.Input + *usage.Cached, Cached: *usage.Cached, CacheWrite: *usage.CacheWrite, Output: *usage.Output}
		want[model] = v
		total.Requests += v.Requests
		total.Input += v.Input
		total.Cached += v.Cached
		total.CacheWrite += v.CacheWrite
		total.Output += v.Output
	}
	if total.Input != *host.Usage.Input+*host.Usage.Cached || total.Output != *host.Usage.Output || total.Cached != *host.Usage.Cached || total.CacheWrite != *host.Usage.CacheWrite {
		return nil, nil, 0, fmt.Errorf("Grok parent prompt and model usage differ")
	}
	var observed struct {
		Events       []dashEvent `json:"events"`
		Applications []struct {
			Kind         string `json:"kind"`
			State        string `json:"state"`
			CallID       string `json:"callId"`
			HasResult    bool   `json:"hasResult"`
			CapabilityID string `json:"capabilityId"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(snapshot, &observed); err != nil {
		return nil, nil, 0, err
	}
	calls := map[string]bool{}
	for _, app := range observed.Applications {
		if app.Kind != "subagent" || !isChildLaunch(app.CapabilityID) {
			continue
		}
		if app.State != "verified" || !app.HasResult || app.CallID == "" || calls[app.CallID] {
			return nil, nil, 0, fmt.Errorf("Grok child call lacks a unique paired result")
		}
		calls[app.CallID] = true
	}
	if len(calls) == 0 {
		return nil, nil, 0, fmt.Errorf("Grok child call missing")
	}
	var requests []dashEvent
	for _, event := range observed.Events {
		if event.Reason == "not_llm_path" || event.UsageMissing == "not_called" {
			continue
		}
		if event.Usage == nil || event.UsagePartial || event.Usage.InputTokens == nil || event.Usage.OutputTokens == nil || event.Seq == 0 || event.SentModel == "" {
			return nil, nil, 0, fmt.Errorf("Grok parent-child request usage incomplete")
		}
		requests = append(requests, event)
	}
	if len(requests) < 2 || len(requests) > 16 || total.Requests >= len(requests) {
		return nil, nil, 0, fmt.Errorf("Grok parent-child request count outside verifiable range")
	}
	matches, selected := 0, 0
	for mask := 1; mask < (1 << len(requests)); mask++ {
		if bits.OnesCount(uint(mask)) != total.Requests {
			continue
		}
		got := map[string]ModelUsage{}
		for i, event := range requests {
			if mask&(1<<i) == 0 {
				continue
			}
			v := got[event.SentModel]
			v.Requests++
			v.Input += deref(event.Usage.InputTokens)
			v.Cached += deref(event.Usage.CachedTokens)
			v.CacheWrite += deref(event.Usage.CacheWriteTokens)
			v.Output += deref(event.Usage.OutputTokens)
			got[event.SentModel] = v
		}
		if sameModelUsage(got, want) {
			matches++
			selected = mask
		}
	}
	if matches != 1 || len(requests)-total.Requests < len(calls) {
		return nil, nil, 0, fmt.Errorf("Grok parent-child usage partition is not unique or child requests missing")
	}
	for i, event := range requests {
		if selected&(1<<i) != 0 {
			parent = append(parent, event.Seq)
			continue
		}
		child = append(child, event.Seq)
		childTokens += deref(event.Usage.InputTokens) + deref(event.Usage.OutputTokens)
		for _, attempt := range event.JevAttempts {
			if !attempt.Cached {
				childTokens += deref(attempt.InputTokens) + deref(attempt.OutputTokens)
			}
		}
	}
	return parent, child, childTokens, nil
}

func sameModelUsage(got, want map[string]ModelUsage) bool {
	if len(got) != len(want) {
		return false
	}
	for model, usage := range want {
		if got[model] != usage {
			return false
		}
	}
	return true
}

func isChildLaunch(id string) bool {
	id = strings.ToLower(id)
	for _, name := range []string{":spawn_agent@", ":spawn_subagent@", ":run_subagent@", ":agent@", ":task@"} {
		if strings.Contains(id, name) {
			return true
		}
	}
	return false
}
