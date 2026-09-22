package proxy

import (
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const (
	ClassVerified    = "verified"
	ClassDelivered   = "delivered"
	ClassStarted     = "started"
	ClassRewritten   = "rewritten"
	ClassFailed      = "failed"
	ClassSelected    = "selected"
	ClassObserve     = "observe"
	ClassOff         = "off"
	ClassUnsupported = "unsupported"
	ClassUnobserved  = "unobserved"
)

var classOrder = []string{
	plan.KindModel, plan.KindSubagent, plan.KindSkill, plan.KindMCP, plan.KindCLI, plan.KindPlugin, "compaction",
}

var classLabels = map[string]string{
	plan.KindModel:    "モデルとeffort",
	plan.KindSubagent: "子エージェント",
	plan.KindSkill:    "スキル",
	plan.KindMCP:      "MCPツール",
	plan.KindCLI:      "CLI",
	plan.KindPlugin:   "プラグイン",
	"compaction":      "履歴圧縮",
}

type ClassCell struct {
	Kind     string         `json:"kind"`
	Label    string         `json:"label"`
	Status   string         `json:"status"`
	Count    int            `json:"count"`
	Verified int            `json:"verified"`
	Hosts    map[string]int `json:"hosts,omitempty"`
	Evidence string         `json:"evidence,omitempty"`
}

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
	ByClass         []ClassCell    `json:"by_class,omitempty"`
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

type classAcc struct {
	count, verified int
	hosts           map[string]int
	best            string
	evidence        string
}

func ClassMap(apps []*Application, events []Event, opt Options, defaultHost string) []ClassCell {
	by := map[string]*classAcc{}
	for _, k := range classOrder {
		by[k] = &classAcc{hosts: map[string]int{}}
	}
	hostOf := func(h string) string {
		if h != "" {
			return h
		}
		return defaultHost
	}
	bump := func(kind, host, state, evidence string) {
		a := by[kind]
		if a == nil {
			return
		}
		a.count++
		if host != "" {
			a.hosts[host]++
		}
		if state == AppVerified {
			a.verified++
		}
		if classRank(state) > classRank(a.best) {
			a.best = state
			if evidence != "" {
				a.evidence = clipEvent(evidence)
			}
		}
	}
	for _, app := range apps {
		if app == nil {
			continue
		}
		h := hostOf(app.Host)
		ev := app.CapabilityID
		if ev == "" {
			ev = app.Kind
		}
		bump(app.Kind, h, app.State, ev)
		if app.PluginOf != "" {
			bump(plan.KindPlugin, h, app.State, app.PluginOf)
		}
	}
	for _, e := range events {
		h := hostOf(e.Host)
		if e.ReasoningChanged || modelRewritten(e) {
			msg := "effort"
			if modelRewritten(e) {
				msg = e.OriginalModel + "→" + e.SentModel
			}
			bump(plan.KindModel, h, ClassRewritten, msg)
		}
		if e.CompactApplied {
			bump("compaction", h, ClassRewritten, "compact")
		}
	}
	out := make([]ClassCell, 0, len(classOrder))
	for _, k := range classOrder {
		a := by[k]
		cell := ClassCell{
			Kind: k, Label: classLabels[k], Status: classStatus(k, a, opt),
			Count: a.count, Verified: a.verified, Evidence: a.evidence,
		}
		if len(a.hosts) > 0 {
			cell.Hosts = a.hosts
		}
		out = append(out, cell)
	}
	return out
}

func modelRewritten(e Event) bool {
	return e.OriginalModel != "" && e.SentModel != "" && e.OriginalModel != e.SentModel
}

func classRank(state string) int {
	switch state {
	case AppVerified:
		return 90
	case ClassRewritten:
		return 80
	case AppDelivered:
		return 70
	case AppStarted, AppResultReceived:
		return 60
	case AppFailed:
		return 40
	case AppSelected:
		return 30
	case AppUnknown:
		return 20
	default:
		return 0
	}
}

func classStatus(kind string, a *classAcc, opt Options) string {
	if a == nil {
		return ClassUnobserved
	}
	if a.verified > 0 {
		return ClassVerified
	}
	switch a.best {
	case AppDelivered:
		return ClassDelivered
	case AppStarted, AppResultReceived:
		return ClassStarted
	case ClassRewritten:
		return ClassRewritten
	case AppFailed:
		return ClassFailed
	case AppSelected:
		return ClassSelected
	}
	if kind == "compaction" {
		if opt.Compaction == CompactionOff {
			return ClassOff
		}
		return ClassUnobserved
	}
	mode := opt.KindMode(kind)
	switch mode {
	case KindOff:
		return ClassOff
	case KindApply:
		if kind == plan.KindModel {
			return ClassUnsupported
		}
		return ClassUnobserved
	default:
		return ClassObserve
	}
}
