package proxy

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const (
	ModeBaseline = "baseline"
	ModeFilter   = "filter"
	ModeForced   = "forced"

	CompactionOff = "off"
	CompactionOn  = "on"

	ReasoningPreserve = "preserve"
	ReasoningLegacy   = "legacy"

	SelectionLocal  = "local"
	SelectionJev    = "jev"
	SelectionHybrid = "hybrid"

	PolicyRequired = "required"
	PolicyFallback = "fallback"

	KindOff     = "off"
	KindObserve = "observe"
	KindApply   = "apply"
	KindFixed   = "fixed"
)

// Options are resolved once at process start. Invalid values fail startup.
type Options struct {
	Mode              string
	Compaction        string
	Reasoning         string
	SelectionMode     string
	RunID             string
	ArgsModel         string
	ArgsTools         map[string]bool
	DirectTools       map[string]bool
	AutoApply         bool
	ApplicationPolicy string
	KindModes         map[string]string
	AfterRewrite      func(context.Context, []byte) []byte
	Shadow            bool
	// ClaudeAdvise enables the Jev call on Claude's advise path (reminder-only
	// hint, no tool_choice narrowing). Off by default: it costs a Jev judgment
	// without narrowing tools[], a net token cost. See docs/MEMO.md.
	ClaudeAdvise bool
	// ClaudeClearToolUses adds the clear_tool_uses_20250919 context-management
	// edit to Claude requests so old tool_use/tool_result pairs are cleared
	// server-side once the trigger is hit. Off by default; see docs/MEMO.md.
	ClaudeClearToolUses bool
	ClaudeClearTrigger  int
	ClaudeClearAtLeast  int
	ClaudeClearKeep     int
	// ClaudeClearExclude names tools the clear_tool_uses_20250919 edit must
	// never clear (Anthropic's exclude_tools field). Empty by default, which
	// omits the key entirely; see docs/MEMO.md.
	ClaudeClearExclude []string
	// ClaudeClearGate decides whether ClaudeClearToolUses actually adds the
	// edit: "off" adds it on every request (the original behavior), "jev" asks
	// Jev once per conversation whether old tool results will be needed again.
	ClaudeClearGate string
	Transforms      TransformOptions
	CostGateMax     int
	// hints is shared by every copy of these Options (one per proxy server).
	hints *hintStore
	// clearGates is shared like hints: per-conversation clear-gate decisions.
	clearGates *clearGateStore
}

func DefaultOptions() Options {
	return Options{
		Mode:               ModeFilter,
		Compaction:         CompactionOn,
		Reasoning:          ReasoningLegacy,
		SelectionMode:      SelectionHybrid,
		ArgsTools:          map[string]bool{},
		DirectTools:        map[string]bool{},
		ApplicationPolicy:  "",
		KindModes:          defaultKindModes(),
		Transforms:         defaultTransforms(),
		ClaudeClearTrigger: defaultClearTrigger,
		ClaudeClearAtLeast: defaultClearAtLeast,
		ClaudeClearKeep:    defaultClearKeep,
		ClaudeClearGate:    ClearGateOff,
		hints:              newHintStore(),
		clearGates:         newClearGateStore(),
	}
}

func defaultKindModes() map[string]string {
	return map[string]string{
		"model":    KindObserve,
		"subagent": KindObserve,
		"skill":    KindObserve,
		"mcp_tool": KindObserve,
		"cli":      KindObserve,
		"plugin":   KindObserve,
		"ateam":    KindFixed,
	}
}

func OptionsFromEnv() (Options, error) {
	o := DefaultOptions()
	if v := strings.TrimSpace(os.Getenv("JEV_ROUTING_MODE")); v != "" {
		switch v {
		case ModeBaseline, ModeFilter, ModeForced:
			o.Mode = v
		default:
			return o, fmt.Errorf("invalid JEV_ROUTING_MODE %q (baseline|filter|forced)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_COMPACTION")); v != "" {
		switch v {
		case CompactionOff, CompactionOn:
			o.Compaction = v
		default:
			return o, fmt.Errorf("invalid JEV_COMPACTION %q (off|on)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_REASONING")); v != "" {
		switch v {
		case ReasoningPreserve, ReasoningLegacy:
			o.Reasoning = v
		default:
			return o, fmt.Errorf("invalid JEV_REASONING %q (preserve|legacy)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_COST_GATE_MAX")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return o, fmt.Errorf("invalid JEV_COST_GATE_MAX %q", v)
		}
		o.CostGateMax = n
	} else {
		o.CostGateMax = decidedCostGateMaxCandidates
	}
	if v := strings.TrimSpace(os.Getenv("JEV_SHADOW")); v != "" {
		switch v {
		case "1", "true", "on":
			o.Shadow = true
		case "0", "false", "off":
			o.Shadow = false
		default:
			return o, fmt.Errorf("invalid JEV_SHADOW %q (on|off)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_CLAUDE_ADVISE")); v != "" {
		switch v {
		case "1", "true", "on":
			o.ClaudeAdvise = true
		case "0", "false", "off":
			o.ClaudeAdvise = false
		default:
			return o, fmt.Errorf("invalid JEV_CLAUDE_ADVISE %q (on|off)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_CLAUDE_CLEAR_TOOL_USES")); v != "" {
		switch v {
		case "1", "true", "on":
			o.ClaudeClearToolUses = true
		case "0", "false", "off":
			o.ClaudeClearToolUses = false
		default:
			return o, fmt.Errorf("invalid JEV_CLAUDE_CLEAR_TOOL_USES %q (on|off)", v)
		}
	}
	for env, dst := range map[string]*int{
		"JEV_CLAUDE_CLEAR_TRIGGER":  &o.ClaudeClearTrigger,
		"JEV_CLAUDE_CLEAR_AT_LEAST": &o.ClaudeClearAtLeast,
		"JEV_CLAUDE_CLEAR_KEEP":     &o.ClaudeClearKeep,
	} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return o, fmt.Errorf("invalid %s %q", env, v)
			}
			*dst = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_CLAUDE_CLEAR_EXCLUDE")); v != "" {
		names, err := parseToolNameList(v)
		if err != nil {
			return o, fmt.Errorf("invalid JEV_CLAUDE_CLEAR_EXCLUDE %q: %w", v, err)
		}
		o.ClaudeClearExclude = names
	}
	if v := strings.TrimSpace(os.Getenv("JEV_CLAUDE_CLEAR_GATE")); v != "" {
		switch v {
		case ClearGateOff, ClearGateJev:
			o.ClaudeClearGate = v
		default:
			return o, fmt.Errorf("invalid JEV_CLAUDE_CLEAR_GATE %q (off|jev)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_TRANSFORMS")); v != "" {
		tr, err := parseTransforms(v)
		if err != nil {
			return o, err
		}
		o.Transforms = tr
	}
	if v := strings.TrimSpace(os.Getenv("JEV_SELECTION_MODE")); v != "" {
		switch v {
		case SelectionLocal, SelectionJev, SelectionHybrid:
			o.SelectionMode = v
		default:
			return o, fmt.Errorf("invalid JEV_SELECTION_MODE %q (local|jev|hybrid)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_RUN_ID")); v != "" {
		if err := validateRunID(v); err != nil {
			return o, err
		}
		o.RunID = v
	}
	argsModel := strings.TrimSpace(os.Getenv("JEV_ARGS_MODEL"))
	argsTools := strings.TrimSpace(os.Getenv("JEV_ARGS_TOOLS"))
	direct := strings.TrimSpace(os.Getenv("JEV_DIRECT_TOOLS"))

	if (argsModel == "") != (argsTools == "") {
		return o, fmt.Errorf("JEV_ARGS_MODEL and JEV_ARGS_TOOLS must be set together")
	}
	if argsModel != "" {
		tools, err := parseNameList(argsTools)
		if err != nil {
			return o, err
		}
		if o.Mode != ModeForced {
			return o, fmt.Errorf("JEV_ARGS_MODEL requires JEV_ROUTING_MODE=forced")
		}
		o.ArgsModel = argsModel
		o.ArgsTools = tools
	}
	if v := strings.TrimSpace(os.Getenv("JEV_AUTO_APPLY")); v != "" {
		switch v {
		case "1", "true", "on":
			o.AutoApply = true
		case "0", "false", "off":
			o.AutoApply = false
		default:
			return o, fmt.Errorf("invalid JEV_AUTO_APPLY %q (on|off)", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_APPLICATION_POLICY")); v != "" {
		switch v {
		case PolicyRequired, PolicyFallback:
			o.ApplicationPolicy = v
		default:
			return o, fmt.Errorf("invalid JEV_APPLICATION_POLICY %q (required|fallback)", v)
		}
	} else if o.AutoApply {
		o.ApplicationPolicy = PolicyRequired
	}
	if o.AutoApply {
		for _, k := range []string{"skill", "mcp_tool", "cli", "plugin"} {
			if o.KindModes[k] == KindObserve {
				o.KindModes[k] = KindApply
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("JEV_KIND_MODES")); v != "" {
		for _, part := range strings.Split(v, ",") {
			kind, mode, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || kind == "" {
				return o, fmt.Errorf("invalid JEV_KIND_MODES %q", v)
			}
			switch mode {
			case KindOff, KindObserve, KindApply, KindFixed:
				o.KindModes[kind] = mode
			default:
				return o, fmt.Errorf("invalid kind mode %q", mode)
			}
		}
	}
	if direct != "" {
		tools, err := parseNameList(direct)
		if err != nil {
			return o, err
		}
		if o.Mode != ModeForced {
			return o, fmt.Errorf("JEV_DIRECT_TOOLS requires JEV_ROUTING_MODE=forced")
		}
		if o.ArgsModel != "" {
			return o, fmt.Errorf("JEV_DIRECT_TOOLS cannot be combined with JEV_ARGS_MODEL")
		}
		o.DirectTools = tools
	}
	return o, nil
}

func parseNameList(s string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("empty tool name in list")
		}
		if out[name] {
			return nil, fmt.Errorf("duplicate tool name %q", name)
		}
		out[name] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty tool list")
	}
	return out, nil
}

// parseToolNameList splits a comma-separated tool name list, trimming
// whitespace and dropping empty entries. Unlike parseNameList it returns an
// ordered slice (exclude_tools is a JSON array, not a set) and allows
// duplicates.
func parseToolNameList(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty tool list")
	}
	return out, nil
}

func validateRunID(s string) error {
	if len(s) == 0 || len(s) > 64 {
		return fmt.Errorf("JEV_RUN_ID must be 1-64 characters")
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("JEV_RUN_ID contains invalid character")
	}
	return nil
}

func (o Options) ArgsToolSet() map[string]bool {
	if o.ArgsTools == nil {
		return map[string]bool{}
	}
	return o.ArgsTools
}

func (o Options) DirectToolSet() map[string]bool {
	if o.DirectTools == nil {
		return map[string]bool{}
	}
	return o.DirectTools
}

func (o Options) KindMode(kind string) string {
	if o.KindModes != nil {
		if m, ok := o.KindModes[kind]; ok {
			return m
		}
	}
	if kind == "ateam" {
		return KindFixed
	}
	return KindObserve
}
