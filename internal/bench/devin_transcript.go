package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// readDevinTranscript keeps only aggregate host-side usage. The transcript
// itself contains conversation text and stays in the disposable sandbox.
func readDevinTranscript(path string) (*HostTranscriptUsage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Devin transcript unavailable: %w", err)
	}
	var data struct {
		SchemaVersion   string `json:"schema_version"`
		SessionID       string `json:"session_id"`
		UsageIncomplete bool   `json:"usage_is_incomplete"`
		Continuation    string `json:"continued_trajectory_ref"`
		Agent           struct {
			ModelName string `json:"model_name"`
		} `json:"agent"`
		Steps []struct {
			Source       string `json:"source"`
			ModelName    string `json:"model_name"`
			LLMCallCount *int   `json:"llm_call_count"`
			Observation  struct {
				Results []struct {
					Subagents []json.RawMessage `json:"subagent_trajectory_ref"`
				} `json:"results"`
			} `json:"observation"`
			Metrics *struct {
				Prompt     *int `json:"prompt_tokens"`
				Completion *int `json:"completion_tokens"`
				Cached     *int `json:"cached_tokens"`
			} `json:"metrics"`
		} `json:"steps"`
		Subagents    []json.RawMessage `json:"subagent_trajectories"`
		FinalMetrics struct {
			Prompt     *int `json:"total_prompt_tokens"`
			Completion *int `json:"total_completion_tokens"`
			Cached     *int `json:"total_cached_tokens"`
			Steps      *int `json:"total_steps"`
		} `json:"final_metrics"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("Devin transcript invalid JSON: %w", err)
	}
	m := data.FinalMetrics
	if !strings.HasPrefix(data.SchemaVersion, "ATIF-v1.") || data.SessionID == "" || len(data.Steps) == 0 || m.Prompt == nil || m.Completion == nil || m.Cached == nil || m.Steps == nil {
		return nil, fmt.Errorf("Devin transcript missing session, steps, or final metrics")
	}
	if data.UsageIncomplete {
		return nil, fmt.Errorf("Devin transcript usage is incomplete")
	}
	if *m.Prompt < 0 || *m.Completion < 0 || *m.Cached < 0 || *m.Cached > *m.Prompt || *m.Steps != len(data.Steps) || *m.Prompt+*m.Completion == 0 {
		return nil, fmt.Errorf("Devin transcript final metrics invalid")
	}
	if len(data.Subagents) > 0 || data.Continuation != "" {
		return nil, fmt.Errorf("Devin transcript subagent usage is not attributable")
	}
	var prompt, completion, cached, agents int
	models := map[string]bool{}
	for _, step := range data.Steps {
		for _, result := range step.Observation.Results {
			if len(result.Subagents) > 0 {
				return nil, fmt.Errorf("Devin transcript subagent usage is not attributable")
			}
		}
		if step.Source != "agent" {
			continue
		}
		if step.LLMCallCount != nil && *step.LLMCallCount == 0 && step.Metrics == nil {
			continue
		}
		agents++
		model := step.ModelName
		if model == "" {
			model = data.Agent.ModelName
		}
		if model == "" || model != strings.TrimSpace(model) {
			return nil, fmt.Errorf("Devin transcript agent model missing or invalid")
		}
		models[model] = true
		if step.Metrics == nil || step.Metrics.Prompt == nil || step.Metrics.Completion == nil {
			return nil, fmt.Errorf("Devin transcript agent step metrics missing")
		}
		p, c := *step.Metrics.Prompt, *step.Metrics.Completion
		ca := 0
		if step.Metrics.Cached != nil {
			ca = *step.Metrics.Cached
		}
		if p < 0 || c < 0 || ca < 0 || ca > p {
			return nil, fmt.Errorf("Devin transcript agent step metrics invalid")
		}
		prompt += p
		completion += c
		cached += ca
	}
	if agents == 0 || prompt != *m.Prompt || completion != *m.Completion || cached != *m.Cached {
		return nil, fmt.Errorf("Devin transcript step and final metrics differ")
	}
	modelNames := make([]string, 0, len(models))
	for model := range models {
		modelNames = append(modelNames, model)
	}
	sort.Strings(modelNames)
	return &HostTranscriptUsage{
		PromptTokens: *m.Prompt, CompletionTokens: *m.Completion,
		CachedTokens: *m.Cached, Steps: *m.Steps, ModelNames: modelNames,
	}, nil
}
