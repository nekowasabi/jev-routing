package proxy

import "github.com/nekowasabi/jev-routing/internal/plan"

type DashboardMetrics struct {
	Selected        int            `json:"selected"`
	Delivered       int            `json:"delivered"`
	Started         int            `json:"started"`
	ResultReceived  int            `json:"result_received"`
	Verified        int            `json:"verified"`
	Failed          int            `json:"failed"`
	Unknown         int            `json:"unknown"`
	Awaiting        int            `json:"awaiting_approval"`
	Unapplied       int            `json:"unapplied"`
	LocalSkip       int            `json:"local_skip"`
	JevCalls        int            `json:"jev_calls"`
	JevOK           int            `json:"jev_ok"`
	MissingUsage    int            `json:"missing_usage"`
	UnappliedReason map[string]int `json:"unapplied_reason"`
	ByKind          map[string]int `json:"by_kind"`
	Sample          bool           `json:"sample,omitempty"`
}

func MetricsFromApps(apps []*Application) DashboardMetrics {
	out := DashboardMetrics{UnappliedReason: map[string]int{}, ByKind: map[string]int{}}
	for _, app := range apps {
		if app == nil {
			continue
		}
		out.ByKind[app.Kind]++
		switch app.State {
		case AppSelected:
			out.Selected++
			out.Unapplied++
			out.UnappliedReason["selected_not_delivered"]++
		case AppDelivered:
			out.Delivered++
		case AppStarted:
			out.Started++
		case AppResultReceived:
			out.ResultReceived++
		case AppVerified:
			out.Verified++
		case AppFailed:
			out.Failed++
			out.Unapplied++
			out.UnappliedReason["failed"]++
		case AppUnknown:
			out.Unknown++
			out.Unapplied++
			out.UnappliedReason["unknown"]++
		case AppAwaitingApproval:
			out.Awaiting++
			out.Unapplied++
			out.UnappliedReason["awaiting_approval"]++
		}
		if app.Kind == plan.KindSkill && app.State == AppDelivered {
			out.Selected++
		}
	}
	return out
}

func MetricsFromEvents(events []Event) DashboardMetrics {
	out := DashboardMetrics{UnappliedReason: map[string]int{}, ByKind: map[string]int{}}
	for _, e := range events {
		out.JevCalls += e.JevCalls
		if e.JevFailed == 0 && e.JevCalls > 0 {
			out.JevOK += e.JevCalls - e.JevCached
		}
		if e.UsageMissing != "" {
			out.MissingUsage++
		}
		if e.Source == "local" && e.Changed && e.SelectionJevCalls == 0 {
			out.LocalSkip++
		}
		if e.Changed && e.Apply == "none" {
			out.Unapplied++
			reason := e.Reason
			if reason == "" {
				reason = "unapplied"
			}
			out.UnappliedReason[reason]++
		}
	}
	return out
}

func MergeMetrics(a, b DashboardMetrics) DashboardMetrics {
	out := a
	if out.UnappliedReason == nil {
		out.UnappliedReason = map[string]int{}
	}
	if out.ByKind == nil {
		out.ByKind = map[string]int{}
	}
	out.Selected += b.Selected
	out.Delivered += b.Delivered
	out.Started += b.Started
	out.ResultReceived += b.ResultReceived
	out.Verified += b.Verified
	out.Failed += b.Failed
	out.Unknown += b.Unknown
	out.Awaiting += b.Awaiting
	out.Unapplied += b.Unapplied
	out.LocalSkip += b.LocalSkip
	out.JevCalls += b.JevCalls
	out.JevOK += b.JevOK
	out.MissingUsage += b.MissingUsage
	out.Sample = out.Sample || b.Sample
	for k, v := range b.UnappliedReason {
		out.UnappliedReason[k] += v
	}
	for k, v := range b.ByKind {
		out.ByKind[k] += v
	}
	return out
}

func EffectFromComparison(current, baseline *int) string {
	if current == nil || baseline == nil {
		return "missing"
	}
	if *current < *baseline {
		return "decrease"
	}
	if *current > *baseline {
		return "increase"
	}
	return "unchanged"
}
