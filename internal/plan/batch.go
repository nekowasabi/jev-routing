package plan

import (
	"context"
	"time"
)

type BatchItem struct {
	ID      string
	Request RouteRequest
}

type BatchResult struct {
	ID        string
	Route     RouteResult
	Asked     bool
	Cached    bool
	Fallback  bool
	Missing   bool
	InputTok  *int
	OutputTok *int
}

type BatchAsker func(ctx context.Context, questions map[string]map[string]string) (answers map[string]string, conf map[string]float64, usageIn, usageOut *int, cached bool, err error)

func RouteBatch(ctx context.Context, items []BatchItem, ask BatchAsker, deadline time.Time) []BatchResult {
	out := make([]BatchResult, len(items))
	pending := map[string]int{}
	questions := map[string]map[string]string{}
	for i, item := range items {
		local := Route(item.Request, nil)
		out[i] = BatchResult{ID: item.ID, Route: local}
		if local.Outcome == RouteSelected {
			continue
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			out[i].Fallback = true
			out[i].Route.ReasonCode = ReasonAskTimeout
			continue
		}
		criteria := map[string]string{NoMatchID: "none of the candidates fit"}
		for _, c := range item.Request.Catalog.Eligible() {
			desc := c.Description
			if desc == "" {
				desc = c.Name
			}
			criteria[c.ID] = desc
		}
		questions[item.ID] = criteria
		pending[item.ID] = i
	}
	if len(pending) == 0 || ask == nil {
		return out
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	answers, confs, inTok, outTok, cached, err := ask(ctx, questions)
	for id, idx := range pending {
		out[idx].Asked = true
		out[idx].Cached = cached
		out[idx].InputTok = inTok
		out[idx].OutputTok = outTok
		if err != nil {
			out[idx].Fallback = true
			if ctx.Err() != nil {
				out[idx].Route.ReasonCode = ReasonAskTimeout
			} else {
				out[idx].Route.ReasonCode = ReasonAskFailed
			}
			continue
		}
		choice, ok := answers[id]
		if !ok || choice == "" {
			out[idx].Missing = true
			out[idx].Fallback = true
			out[idx].Route.ReasonCode = ReasonMissingAnswer
			continue
		}
		conf := 0.0
		if confs != nil {
			conf = confs[id]
		}
		got := Route(items[idx].Request, func(string, map[string]string) (string, float64, error) {
			return choice, conf, nil
		})
		out[idx].Route = got
		if got.Outcome != RouteSelected {
			out[idx].Fallback = true
		}
	}
	return out
}
