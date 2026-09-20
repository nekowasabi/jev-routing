package plan

import (
	"fmt"
	"strings"
)

type SubagentRequest struct {
	Text            string
	Brief           string
	Host            string
	Catalog         Catalog
	ExplicitRoles   []string
	RunningRoles    []string
	ForbidDelegate  bool
	ApprovedScope   string
	AdoptConfidence float64
}

type SubagentResult struct {
	Outcome      string
	CapabilityID string
	Role         string
	Launcher     string
	Brief        string
	ReasonCode   string
	Asked        bool
}

type HistoryItem struct {
	MessageID    string
	AssignmentID string
	Kind         string // progress | final | fail
	Body         string
}

func RouteSubagent(req SubagentRequest, ask ChoiceAsker) SubagentResult {
	if req.AdoptConfidence == 0 {
		req.AdoptConfidence = 0.85
	}
	out := SubagentResult{Outcome: RouteNoDecision, Brief: req.Brief}
	if req.ForbidDelegate {
		out.ReasonCode = "forbid_delegate"
		return out
	}
	if strings.TrimSpace(req.Brief) == "" {
		out.ReasonCode = "missing_brief"
		return out
	}
	candidates := []Capability{}
	for _, item := range req.Catalog.Items {
		if item.Kind != KindSubagent || item.Availability != AvailInstalled {
			continue
		}
		if item.Target.SubagentLauncher == "" {
			continue
		}
		if running(req.RunningRoles, item.Name) {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(req.ExplicitRoles) > 0 {
		for _, item := range candidates {
			for _, role := range req.ExplicitRoles {
				if item.Name == role || item.ID == role {
					return selectedSub(item, req.Brief, ReasonExplicit)
				}
			}
		}
		out.ReasonCode = ReasonNoMatch
		return out
	}
	if len(candidates) == 0 {
		out.ReasonCode = ReasonNoMatch
		return out
	}
	if len(candidates) == 1 {
		return selectedSub(candidates[0], req.Brief, ReasonLocalSelected)
	}
	if ask == nil {
		out.ReasonCode = ReasonNoMatch
		return out
	}
	criteria := map[string]string{NoMatchID: "no subagent"}
	for _, item := range candidates {
		criteria[item.ID] = item.Name + " " + item.Description
	}
	out.Asked = true
	choice, conf, err := ask(req.Text, criteria)
	if err != nil || strings.TrimSpace(choice) == "" {
		out.ReasonCode = ReasonAskFailed
		if choice == "" && err == nil {
			out.ReasonCode = ReasonMissingAnswer
		}
		return out
	}
	if choice == NoMatchID {
		out.ReasonCode = ReasonNoMatch
		return out
	}
	if conf < req.AdoptConfidence {
		out.ReasonCode = ReasonLowConfidence
		return out
	}
	item, ok := Lookup(req.Catalog, choice)
	if !ok || item.Kind != KindSubagent {
		out.ReasonCode = ReasonInvalidID
		return out
	}
	return selectedSub(item, req.Brief, "jev")
}

func selectedSub(item Capability, brief, reason string) SubagentResult {
	return SubagentResult{
		Outcome:      RouteSelected,
		CapabilityID: item.ID,
		Role:         item.Name,
		Launcher:     item.Target.SubagentLauncher,
		Brief:        brief,
		ReasonCode:   reason,
	}
}

func running(roles []string, name string) bool {
	for _, r := range roles {
		if r == name {
			return true
		}
	}
	return false
}

type Recovered struct {
	Kind         string
	MessageID    string
	AssignmentID string
	Body         string
	Matched      bool
}

func RecoverSubagent(history []HistoryItem, assignmentID string) Recovered {
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h.AssignmentID != assignmentID {
			continue
		}
		return Recovered{Kind: h.Kind, MessageID: h.MessageID, AssignmentID: h.AssignmentID, Body: h.Body, Matched: true}
	}
	return Recovered{}
}

func ChildCommandAllowed(cmd []string, approved []string) error {
	if len(cmd) == 0 {
		return fmt.Errorf("empty command")
	}
	joined := strings.Join(cmd, " ")
	for _, a := range approved {
		if joined == a || cmd[0] == a {
			return nil
		}
	}
	return fmt.Errorf("unapproved child command")
}
