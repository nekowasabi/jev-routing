package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const (
	RouteSelected   = "selected"
	RouteNoDecision = "no_decision"

	ReasonNoMatch        = "no_match"
	ReasonInvalidID      = "invalid_id"
	ReasonMissingAnswer  = "missing_answer"
	ReasonVersionChanged = "catalog_version_changed"
	ReasonExplicit       = "explicit"
	ReasonIneligible     = "ineligible"
	ReasonLocalSelected  = "local_selected"
	ReasonAskFailed      = "ask_failed"
	NoMatchID            = "no_match"
)

type RouteRequest struct {
	RequestID        string
	Text             string
	Host             host.ID
	Catalog          Catalog
	ExpectedRevision string
	Explicit         []string
	Actions          []Action
	NewRequest       bool
}

type RouteResult struct {
	DecisionID      string `json:"decision_id"`
	Outcome         string `json:"outcome"`
	Source          string `json:"source"`
	ReasonCode      string `json:"reason_code"`
	CapabilityID    string `json:"capability_id,omitempty"`
	CatalogRevision string `json:"catalog_revision"`
	PolicyRevision  string `json:"policy_revision"`
}

// ChoiceAsker is the Jev (or fake) single-choice entry used by Route.
type ChoiceAsker func(text string, criteria map[string]string) (choice string, conf float64, err error)

func (r RouteRequest) key() string {
	if r.RequestID != "" {
		return r.RequestID
	}
	if r.NewRequest {
		return ""
	}
	sum := sha256.Sum256([]byte(r.Text + "\n" + r.Catalog.Revision))
	return hex.EncodeToString(sum[:8])
}

func DecisionIDFor(req RouteRequest) string {
	if k := req.key(); k != "" {
		sum := sha256.Sum256([]byte(k + ":" + req.Catalog.Revision))
		return hex.EncodeToString(sum[:12])
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("new:%s:%s:%s", req.Text, req.Catalog.Revision, req.Host)))
	return hex.EncodeToString(sum[:12])
}

func Route(req RouteRequest, ask ChoiceAsker) RouteResult {
	rev := req.Catalog.Revision
	if rev == "" {
		rev = req.Catalog.ComputeRevision()
	}
	out := RouteResult{DecisionID: DecisionIDFor(req), CatalogRevision: rev, PolicyRevision: "route-v1", Outcome: RouteNoDecision}
	if req.ExpectedRevision != "" && req.ExpectedRevision != rev {
		out.ReasonCode = ReasonVersionChanged
		return out
	}
	byID := map[string]Capability{}
	for _, item := range req.Catalog.Items {
		byID[item.ID] = item
	}
	if len(req.Explicit) > 0 {
		for _, id := range req.Explicit {
			item, ok := byID[id]
			if !ok || item.Availability != AvailInstalled || !item.Target.bound(item.Kind) {
				out.ReasonCode = ReasonInvalidID
				return out
			}
		}
		out.Outcome = RouteSelected
		out.Source = "local"
		out.ReasonCode = ReasonExplicit
		out.CapabilityID = req.Explicit[0]
		return out
	}
	eligible := req.Catalog.Eligible()
	if len(eligible) == 0 {
		out.ReasonCode = ReasonIneligible
		return out
	}
	specs := make([]Spec, 0, len(eligible))
	nameID := map[string]string{}
	for _, item := range eligible {
		if item.Kind == KindSkill || item.Kind == KindPlugin || item.Kind == KindModel {
			continue
		}
		specs = append(specs, Spec{Name: item.Name, Desc: item.Description})
		nameID[item.Name] = item.ID
	}
	if len(specs) > 0 {
		d := DecideSpecs(req.Text, req.Actions, specs, req.Host)
		if d.Outcome == OutcomeSelected && !d.Passthrough {
			if id := nameID[d.Tool]; id != "" {
				out.Outcome = RouteSelected
				out.Source = "local"
				out.ReasonCode = ReasonLocalSelected
				out.CapabilityID = id
				return out
			}
		}
	}
	if ask == nil {
		out.ReasonCode = ReasonNoMatch
		return out
	}
	criteria := map[string]string{NoMatchID: "none of the candidates fit"}
	for _, item := range eligible {
		criteria[item.ID] = item.Description
		if item.Description == "" {
			criteria[item.ID] = item.Name
		}
	}
	choice, _, err := ask(req.Text, criteria)
	if err != nil {
		out.ReasonCode = ReasonAskFailed
		return out
	}
	if strings.TrimSpace(choice) == "" {
		out.ReasonCode = ReasonMissingAnswer
		return out
	}
	if choice == NoMatchID {
		out.ReasonCode = ReasonNoMatch
		return out
	}
	item, ok := byID[choice]
	if !ok || item.Availability != AvailInstalled {
		out.ReasonCode = ReasonInvalidID
		return out
	}
	out.Outcome = RouteSelected
	out.Source = "jev"
	out.ReasonCode = "jev"
	out.CapabilityID = choice
	return out
}

func Lookup(cat Catalog, id string) (Capability, bool) {
	for _, item := range cat.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Capability{}, false
}
