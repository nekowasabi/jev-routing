package proxy

import (
	"fmt"
	"os"
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
	AfterRewrite      func([]byte) []byte
}

func DefaultOptions() Options {
	return Options{
		Mode:              ModeFilter,
		Compaction:        CompactionOn,
		Reasoning:         ReasoningLegacy,
		SelectionMode:     SelectionHybrid,
		ArgsTools:         map[string]bool{},
		DirectTools:       map[string]bool{},
		ApplicationPolicy: "",
		KindModes:         defaultKindModes(),
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
