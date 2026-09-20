package plan

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const (
	ModeFixed = "fixed"
	ModeAuto  = "auto"

	ReasonBothFixed           = "both_fixed"
	ReasonUnknownFixedDefault = "unknown_fixed_default"
	ReasonNoPair              = "no_pair"
	ReasonLowConfidence       = "low_confidence"
	ReasonAskTimeout          = "ask_timeout"
	ReasonLegacyFallback      = "legacy_fallback"
)

const ModelPairInstructions = "Pick the cheapest, smallest pair that is sufficient for this task. Use a lower cost and difficulty pair for simple work (hello world, echo, one-liner). Use a higher-cost pair only when the work is review, multi-file, or ambiguous. Pick no_match only if none of these pairs can run this request on this host."

type Pair struct {
	ID          string   `json:"id"`
	Model       string   `json:"model"`
	Effort      string   `json:"effort"`
	Hosts       []string `json:"hosts"`
	Difficulty  string   `json:"difficulty,omitempty"`
	Cost        string   `json:"cost,omitempty"`
	Description string   `json:"description,omitempty"`
}

func PairLabel(p Pair) string {
	s := p.Model + " " + p.Effort
	if p.Difficulty != "" {
		s += "; difficulty=" + p.Difficulty
	}
	if p.Cost != "" {
		s += "; cost=" + p.Cost
	}
	if p.Description != "" {
		s += "; " + p.Description
	}
	return s
}

type ModelRequest struct {
	Task, Role      string
	Host            host.ID
	ModelMode       string
	EffortMode      string
	LegacyModel     string
	LegacyEffort    string
	Pairs           []Pair
	AdoptConfidence float64
}

type ModelAsker func(req ModelRequest, criteria map[string]string) (string, float64, error)

type ModelResult struct {
	Model        string  `json:"model"`
	Effort       string  `json:"effort"`
	Source       string  `json:"source"`
	ReasonCode   string  `json:"reason_code"`
	PairID       string  `json:"pair_id,omitempty"`
	Asked        bool    `json:"asked"`
	RejectedID   string  `json:"rejected_id,omitempty"`
	RejectedConf float64 `json:"rejected_conf,omitempty"`
}

func LoadPairs(path string) ([]Pair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Pairs []Pair `json:"pairs"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	return file.Pairs, nil
}

func RouteModel(req ModelRequest, ask ModelAsker) ModelResult {
	if req.ModelMode == "" {
		req.ModelMode = ModeFixed
	}
	if req.EffortMode == "" {
		req.EffortMode = ModeFixed
	}
	if req.AdoptConfidence == 0 {
		req.AdoptConfidence = 0.85
	}
	legacy := ModelResult{Model: req.LegacyModel, Effort: req.LegacyEffort, Source: "legacy", ReasonCode: ReasonLegacyFallback}
	if req.ModelMode == ModeFixed && req.EffortMode == ModeFixed {
		legacy.ReasonCode = ReasonBothFixed
		legacy.Source = "local"
		return legacy
	}
	if (req.ModelMode == ModeFixed && req.LegacyModel == "") || (req.EffortMode == ModeFixed && req.LegacyEffort == "") {
		legacy.ReasonCode = ReasonUnknownFixedDefault
		return legacy
	}
	allowed := filterPairs(req)
	if len(allowed) == 0 {
		legacy.ReasonCode = ReasonNoPair
		return legacy
	}
	if ask == nil {
		return pickOnlyOrLegacy(allowed, legacy)
	}
	criteria := map[string]string{NoMatchID: "none of these pairs can run this request on this host"}
	for _, p := range allowed {
		criteria[p.ID] = PairLabel(p)
	}
	legacy.Asked = true
	choice, conf, err := ask(req, criteria)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			legacy.ReasonCode = ReasonAskTimeout
		}
		return legacy
	}
	if strings.TrimSpace(choice) == "" {
		legacy.ReasonCode = ReasonMissingAnswer
		return legacy
	}
	if choice == NoMatchID {
		legacy.ReasonCode = ReasonNoMatch
		return legacy
	}
	if conf < req.AdoptConfidence {
		legacy.ReasonCode = ReasonLowConfidence
		legacy.RejectedID = choice
		legacy.RejectedConf = conf
		return legacy
	}
	for _, p := range allowed {
		if p.ID == choice {
			return ModelResult{Model: p.Model, Effort: p.Effort, Source: "jev", ReasonCode: "jev", PairID: p.ID, Asked: true}
		}
	}
	legacy.ReasonCode = ReasonInvalidID
	legacy.RejectedID = choice
	legacy.RejectedConf = conf
	return legacy
}

func filterPairs(req ModelRequest) []Pair {
	var out []Pair
	for _, p := range req.Pairs {
		if !hostOK(p.Hosts, req.Host) {
			continue
		}
		if req.ModelMode == ModeFixed && p.Model != req.LegacyModel {
			continue
		}
		if req.EffortMode == ModeFixed && p.Effort != req.LegacyEffort {
			continue
		}
		out = append(out, p)
	}
	return out
}

func hostOK(hosts []string, h host.ID) bool {
	if len(hosts) == 0 {
		return true
	}
	for _, x := range hosts {
		if host.ID(x) == h {
			return true
		}
	}
	return false
}

func pickOnlyOrLegacy(pairs []Pair, legacy ModelResult) ModelResult {
	if len(pairs) == 1 {
		return ModelResult{Model: pairs[0].Model, Effort: pairs[0].Effort, Source: "local", ReasonCode: ReasonLocalSelected, PairID: pairs[0].ID}
	}
	legacy.ReasonCode = ReasonNoMatch
	return legacy
}
