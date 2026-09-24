package bench

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
)

// matchGrokHostUsage checks Grok's prompt-level main-model total against
// completed proxy events. A canceled request has no request-level usage;
// callers must retain that distinction when using the CLI aggregate.
func matchGrokHostUsage(run RunRecord, agentLog string, snapshot ...[]byte) (string, error) {
	raw, err := os.ReadFile(agentLog)
	if err != nil {
		return "", err
	}
	var host struct {
		UsageIncomplete bool `json:"usage_is_incomplete"`
		Usage           *struct {
			Input      *int `json:"input_tokens"`
			Output     *int `json:"output_tokens"`
			Cached     *int `json:"cache_read_input_tokens"`
			CacheWrite *int `json:"cache_creation_input_tokens"`
			Total      *int `json:"total_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			Input      int  `json:"inputTokens"`
			Output     int  `json:"outputTokens"`
			Cached     *int `json:"cacheReadInputTokens"`
			CacheWrite int  `json:"cacheCreationInputTokens"`
			ModelCalls int  `json:"modelCalls"`
		} `json:"modelUsage"`
	}
	if err := json.Unmarshal(raw, &host); err != nil {
		return "", err
	}
	if len(host.ModelUsage) == 0 {
		return "", fmt.Errorf("Grok CLI model usage missing")
	}
	if host.UsageIncomplete {
		return "", fmt.Errorf("Grok CLI usage is incomplete")
	}
	if host.Usage == nil || host.Usage.Input == nil || host.Usage.Output == nil || host.Usage.Cached == nil || host.Usage.CacheWrite == nil || host.Usage.Total == nil {
		return "", fmt.Errorf("Grok CLI prompt usage missing")
	}
	var freshTotal, outputTotal, cachedTotal, cacheWriteTotal int
	for _, model := range host.ModelUsage {
		if model.Cached == nil || model.ModelCalls <= 0 {
			return "", fmt.Errorf("Grok CLI model usage incomplete")
		}
		freshTotal += model.Input
		outputTotal += model.Output
		cachedTotal += *model.Cached
		cacheWriteTotal += model.CacheWrite
	}
	if freshTotal != *host.Usage.Input || outputTotal != *host.Usage.Output || cachedTotal != *host.Usage.Cached || cacheWriteTotal != *host.Usage.CacheWrite || freshTotal+outputTotal+cachedTotal+cacheWriteTotal != *host.Usage.Total {
		return "", fmt.Errorf("Grok CLI prompt and model usage differ")
	}
	canceled := map[string]int{}
	if len(snapshot) > 0 {
		var observed struct {
			Events []struct {
				SentModel string          `json:"sentModel"`
				Usage     json.RawMessage `json:"usage"`
				Canceled  bool            `json:"canceled"`
				Finish    string          `json:"upstreamFinish"`
			} `json:"events"`
		}
		if err := json.Unmarshal(snapshot[0], &observed); err != nil {
			return "", err
		}
		for _, event := range observed.Events {
			if event.Canceled && event.Finish == "canceled" && (len(event.Usage) == 0 || string(event.Usage) == "null") {
				canceled[event.SentModel]++
			}
		}
	}
	var matched string
	for _, reported := range host.ModelUsage {
		cached := *reported.Cached
		input := reported.Input + cached // Grok headless JSON reports uncached input separately.
		for model, observed := range run.ModelUsage {
			if reported.ModelCalls > 0 && reported.ModelCalls == observed.Requests-canceled[model] && input == observed.Input && reported.Output == observed.Output && cached == observed.Cached && reported.CacheWrite == observed.CacheWrite {
				if matched != "" {
					return "", fmt.Errorf("Grok CLI usage matches multiple proxy models")
				}
				matched = model
			}
		}
	}
	if matched == "" {
		// Why: Grok can send a helper call with the same proxy model name as
		// its main calls. A model-wide sum then includes a call omitted from
		// the CLI prompt ledger; require a unique request-level subset.
		if len(snapshot) == 1 && len(host.ModelUsage) == 1 {
			for _, reported := range host.ModelUsage {
				if model, err := matchGrokRequestSubset(reported.ModelCalls, reported.Input+*reported.Cached, *reported.Cached, reported.CacheWrite, reported.Output, snapshot[0]); err == nil {
					return model, nil
				}
			}
		}
		return "", fmt.Errorf("Grok CLI usage differs from proxy models")
	}
	return matched, nil
}

func matchGrokRequestSubset(calls, input, cached, cacheWrite, output int, snapshot []byte) (string, error) {
	var body struct {
		Events []dashEvent `json:"events"`
	}
	if err := json.Unmarshal(snapshot, &body); err != nil {
		return "", err
	}
	var events []dashEvent
	for _, event := range body.Events {
		if event.Reason == "not_llm_path" || event.UsageMissing == "not_called" || (event.Canceled && event.UpstreamFinish == "canceled" && event.Usage == nil) {
			continue
		}
		if event.SentModel == "" || event.Usage == nil || event.UsagePartial || event.Usage.InputTokens == nil || event.Usage.OutputTokens == nil || event.Usage.CachedTokens == nil {
			return "", fmt.Errorf("Grok request-level usage incomplete")
		}
		events = append(events, event)
	}
	if calls <= 0 || len(events) < calls || len(events) > 16 {
		return "", fmt.Errorf("Grok request count outside verifiable range")
	}
	matches, model := 0, ""
	for mask := 1; mask < 1<<len(events); mask++ {
		if bits.OnesCount(uint(mask)) != calls {
			continue
		}
		got := ModelUsage{}
		candidate := ""
		for i, event := range events {
			if mask&(1<<i) == 0 {
				continue
			}
			if candidate != "" && candidate != event.SentModel {
				candidate = ""
				break
			}
			candidate = event.SentModel
			got.Input += *event.Usage.InputTokens
			got.Cached += *event.Usage.CachedTokens
			got.CacheWrite += deref(event.Usage.CacheWriteTokens)
			got.Output += *event.Usage.OutputTokens
		}
		if candidate != "" && got.Input == input && got.Cached == cached && got.CacheWrite == cacheWrite && got.Output == output {
			matches++
			model = candidate
		}
	}
	if matches != 1 {
		return "", fmt.Errorf("Grok prompt usage has %d matching request subsets", matches)
	}
	return model, nil
}
