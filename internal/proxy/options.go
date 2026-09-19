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
)

// Options are resolved once at process start. Invalid values fail startup.
type Options struct {
	Mode        string
	Compaction  string
	Reasoning   string
	RunID       string
	ArgsModel   string
	ArgsTools   map[string]bool
	DirectTools map[string]bool
}

func DefaultOptions() Options {
	return Options{
		Mode:        ModeFilter,
		Compaction:  CompactionOn,
		Reasoning:   ReasoningLegacy,
		ArgsTools:   map[string]bool{},
		DirectTools: map[string]bool{},
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
