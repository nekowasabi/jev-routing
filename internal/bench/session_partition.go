package bench

import (
	"encoding/json"
	"fmt"
)

// partitionHostRequests identifies the parent from its independently reported
// CLI usage when a host gives parent and child the same session identifier.
func partitionHostRequests(agent, agentLog string, snapshot []byte) (parent, child []int64, childTokens int, err error) {
	want, err := reportedHostUsage(agent, agentLog)
	if err != nil {
		return nil, nil, 0, err
	}
	requests, err := hostRequests(snapshot)
	if err != nil {
		return nil, nil, 0, err
	}
	// ponytail: bounded subset search for short A1 tasks; longer runs stay incomparable until the host exposes distinct session IDs.
	if len(requests) < 2 || len(requests) > 16 {
		return nil, nil, 0, fmt.Errorf("parent-child request count outside verifiable range")
	}
	matched := 0
	selected := 0
	for mask := 1; mask < (1<<len(requests))-1; mask++ {
		var sum ModelUsage
		for i, event := range requests {
			if mask&(1<<i) == 0 {
				continue
			}
			sum.Input += deref(event.Usage.InputTokens)
			sum.Cached += deref(event.Usage.CachedTokens)
			sum.CacheWrite += deref(event.Usage.CacheWriteTokens)
			sum.Output += deref(event.Usage.OutputTokens)
		}
		if sum.Input == want.Input && sum.Cached == want.Cached && sum.CacheWrite == want.CacheWrite && sum.Output == want.Output {
			matched++
			selected = mask
		}
	}
	if matched != 1 {
		return nil, nil, 0, fmt.Errorf("parent-child usage partition is not unique")
	}
	for i, event := range requests {
		if selected&(1<<i) != 0 {
			parent = append(parent, event.Seq)
			continue
		}
		child = append(child, event.Seq)
		childTokens += hostRequestTokens(agent, event)
	}
	return parent, child, childTokens, nil
}

// hostRequests is every metered host inference request in a proxy snapshot;
// an incomplete usage record makes any parent/child split unverifiable.
func hostRequests(snapshot []byte) ([]dashEvent, error) {
	var body struct {
		Events []dashEvent `json:"events"`
	}
	if err := json.Unmarshal(snapshot, &body); err != nil {
		return nil, err
	}
	var requests []dashEvent
	for _, event := range body.Events {
		if event.Reason == "not_llm_path" || event.UsageMissing == "not_called" {
			continue
		}
		if event.Usage == nil || event.UsagePartial || event.Usage.InputTokens == nil || event.Usage.OutputTokens == nil || event.Seq == 0 {
			return nil, fmt.Errorf("parent-child request usage incomplete")
		}
		requests = append(requests, event)
	}
	return requests, nil
}

func hostRequestTokens(agent string, event dashEvent) int {
	input := deref(event.Usage.InputTokens) + deref(event.Usage.CacheWriteTokens)
	if !specOf(agent).inputIncludesCached {
		input += deref(event.Usage.CachedTokens)
	}
	tokens := input + deref(event.Usage.OutputTokens)
	for _, attempt := range event.JevAttempts {
		if !attempt.Cached {
			tokens += deref(attempt.InputTokens) + deref(attempt.OutputTokens)
		}
	}
	return tokens
}
