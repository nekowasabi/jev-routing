package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

type routeInput struct {
	Request         string       `json:"request"`
	Host            string       `json:"host"`
	RequestID       string       `json:"request_id"`
	CatalogRevision string       `json:"catalog_revision"`
	Explicit        []string     `json:"explicit"`
	NewRequest      bool         `json:"new_request"`
	Catalog         plan.Catalog `json:"catalog"`
	Role            string       `json:"role"`
	ModelMode       string       `json:"model_mode"`
	EffortMode      string       `json:"effort_mode"`
	LegacyModel     string       `json:"legacy_model"`
	LegacyEffort    string       `json:"effort"`
	Pairs           []plan.Pair  `json:"pairs"`
}

func cmdRoute(args []string, stdin io.Reader, stdout io.Writer) int {
	fs := flag.NewFlagSet("route", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonMode := fs.Bool("json", false, "JSON in/out")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !*jsonMode {
		fmt.Fprintln(os.Stderr, "route requires --json")
		return 2
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var in routeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	h, err := host.Parse(in.Host)
	if err != nil {
		h = host.Claude
	}
	if in.Catalog.Revision == "" {
		in.Catalog.Revision = in.Catalog.ComputeRevision()
	}
	req := plan.RouteRequest{
		RequestID:        in.RequestID,
		Text:             in.Request,
		Host:             h,
		Catalog:          in.Catalog,
		ExpectedRevision: in.CatalogRevision,
		Explicit:         in.Explicit,
		NewRequest:       in.NewRequest,
	}
	var ask plan.ChoiceAsker
	if c := jev.FromEnv(); c != nil && c.Live() {
		ask = func(text string, criteria map[string]string) (string, float64, error) {
			qs := map[string]jev.Question{"capability": {Type: "choice", Instructions: "pick one capability id", Criteria: criteria}}
			res, err := c.AskSelectionContext(nil, map[string]any{"request": text}, qs)
			if err != nil {
				return "", 0, err
			}
			ch, reason := jev.ValidateChoice(res, "capability", criteriaSet(criteria))
			if reason != "" {
				return "", 0, fmt.Errorf("%s", reason)
			}
			return ch.Choice, ch.Conf, nil
		}
	}
	routed := plan.Route(req, ask)
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if in.ModelMode != "" || in.EffortMode != "" || len(in.Pairs) > 0 {
		hID, _ := host.Parse(in.Host)
		payload := map[string]any{
			"route": routed,
			"model": plan.RouteModel(plan.ModelRequest{
				Task: in.Request, Role: in.Role, Host: hID,
				ModelMode: in.ModelMode, EffortMode: in.EffortMode,
				LegacyModel: in.LegacyModel, LegacyEffort: in.LegacyEffort,
				Pairs: in.Pairs,
			}, ask),
		}
		if err := enc.Encode(payload); err != nil {
			return 1
		}
		return 0
	}
	if err := enc.Encode(routed); err != nil {
		return 1
	}
	return 0
}

func criteriaSet(criteria map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range criteria {
		out[k] = true
	}
	return out
}
