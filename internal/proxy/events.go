package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const eventLimit = 1000
const eventStrMax = 200

// Event is one request-level observation. Bodies, args, secrets are never stored.
type Event struct {
	Seq                int64              `json:"seq"`
	InstanceID         string             `json:"instanceId"`
	Ts                 time.Time          `json:"ts"`
	Host               string             `json:"host"`
	SessionKey         string             `json:"sessionKey,omitempty"`
	Source             string             `json:"source"`
	Reason             string             `json:"reason"`
	Apply              string             `json:"apply"`
	Chosen             string             `json:"chosen"`
	Confidence         *float64           `json:"confidence"`
	NeedsTool          *float64           `json:"needsTool"`
	Changed            bool               `json:"changed"`
	OriginalModel      string             `json:"originalModel,omitempty"`
	SentModel          string             `json:"sentModel,omitempty"`
	ToolBefore         int                `json:"toolBefore"`
	ToolAfter          int                `json:"toolAfter"`
	ToolsBefore        []string           `json:"toolsBefore,omitempty"`
	ToolsAfter         []string           `json:"toolsAfter,omitempty"`
	HistoryTypes       []string           `json:"historyTypes,omitempty"`
	UnsupportedHistory []string           `json:"unsupportedHistory,omitempty"`
	HistoryIssues      []string           `json:"historyIssues,omitempty"`
	CompactDropped     int                `json:"compactDropped"`
	CompactApplied     bool               `json:"compactApplied"`
	ReasoningChanged   bool               `json:"reasoningChanged,omitempty"`
	RequestPath        string             `json:"requestPath,omitempty"`
	Method             string             `json:"method,omitempty"`
	ContentType        string             `json:"contentType,omitempty"`
	BodyBytes          int                `json:"bodyBytes,omitempty"`
	JsonValid          *bool              `json:"jsonValid,omitempty"`
	URLHosts           []string           `json:"urlHosts,omitempty"`
	Catalog            *CatalogShape      `json:"catalog,omitempty"`
	JevAttempts        []JevAttempt       `json:"jevAttempts,omitempty"`
	JevCalls           int                `json:"jevCalls"`
	SelectionJevCalls  int                `json:"selectionJevCalls,omitempty"`
	OtherJevCalls      int                `json:"otherJevCalls,omitempty"`
	JevCached          int                `json:"jevCached"`
	JevFailed          int                `json:"jevFailed"`
	UpstreamStatus     *int               `json:"upstreamStatus"`
	UpstreamFinish     string             `json:"upstreamFinish,omitempty"`
	HeaderMs           *float64           `json:"headerMs"`
	BodyMs             *float64           `json:"bodyMs"`
	Usage              *NormalizedUsage   `json:"usage"`
	UsagePartial       bool               `json:"usagePartial,omitempty"`
	UsageMissing       string             `json:"usageMissing,omitempty"`
	SavedTokens        *SavedTokens       `json:"savedTokens,omitempty"`
	Canceled           bool               `json:"canceled,omitempty"`
	Protocol           string             `json:"protocol,omitempty"`
	ConnectFrames      int                `json:"connectFrames,omitempty"`
	Probabilities      map[string]float64 `json:"probabilities,omitempty"`
	CandidateNames     []string           `json:"candidateNames,omitempty"`
	CandidateCount     int                `json:"candidateCount,omitempty"`
	MustKeep           []string           `json:"mustKeep,omitempty"`
	MissingFlags       []string           `json:"missingFlags,omitempty"`
	RuleVersion        string             `json:"ruleVersion,omitempty"`
	Concentration      *float64           `json:"concentration,omitempty"`
	ConnectStatus      string             `json:"connectStatus,omitempty"`
	ObservedTools      []string           `json:"observedTools,omitempty"`
	Engine             string             `json:"engine,omitempty"`
	CompactDelta       int                `json:"compactDelta,omitempty"`
	ProposedKept       []string           `json:"proposedKept,omitempty"`
	Shadow             bool               `json:"shadow,omitempty"`
	ShadowHit          *bool              `json:"shadowHit,omitempty"`
	Misexcluded        []string           `json:"misexcluded,omitempty"`
	Transforms         []string           `json:"transforms,omitempty"`
	ClearedToolUses    int                `json:"clearedToolUses,omitempty"`
	ClearedInputTokens int                `json:"clearedInputTokens,omitempty"`
}

// SavedTokens is an estimate of input tokens avoided before an upstream call.
// It is intentionally separate from Usage, which represents provider-reported consumption.
type SavedTokens struct {
	DirectInput     int `json:"directInput,omitempty"`
	CompactionInput int `json:"compactionInput,omitempty"`
}

type JevAttempt struct {
	Purpose      string  `json:"purpose"`
	Ms           float64 `json:"ms"`
	OK           bool    `json:"ok"`
	Cached       bool    `json:"cached"`
	ErrKind      string  `json:"errKind,omitempty"`
	Status       int     `json:"status,omitempty"`
	Questions    int     `json:"questions,omitempty"`
	InputTokens  *int    `json:"inputTokens,omitempty"`
	OutputTokens *int    `json:"outputTokens,omitempty"`
}

func selectionJevCalls(attempts []JevAttempt) int {
	n := 0
	for _, a := range attempts {
		if a.Purpose == "selection" {
			n++
		}
	}
	return n
}

type EventLog struct {
	mu         sync.Mutex
	InstanceID string
	StartedAt  time.Time
	seq        int64
	items      []Event
}

func newEventLog() *EventLog {
	return &EventLog{InstanceID: newInstanceID(), StartedAt: time.Now().UTC(), items: make([]Event, 0, 64)}
}

func newInstanceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(b[:])
}

func (l *EventLog) NextSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	return l.seq
}

func (l *EventLog) Add(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Seq == 0 {
		l.seq++
		e.Seq = l.seq
	}
	e.InstanceID = l.InstanceID
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	e.Chosen = clipEvent(e.Chosen)
	e.Reason = clipEvent(e.Reason)
	e.Source = clipEvent(e.Source)
	e.Apply = clipEvent(e.Apply)
	e.OriginalModel = clipEvent(e.OriginalModel)
	e.SentModel = clipEvent(e.SentModel)
	e.Method = clipEvent(e.Method)
	e.ContentType = clipEvent(e.ContentType)
	if len(l.items) >= eventLimit {
		l.items = l.items[1:]
	}
	l.items = append(l.items, e)
	return e
}

func (l *EventLog) Update(seq int64, fn func(*Event)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.items {
		if l.items[i].Seq == seq {
			fn(&l.items[i])
			return
		}
	}
}

func (l *EventLog) Snapshot(since int64) (events []Event, recorded int, oldest int64, truncated bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recorded = len(l.items)
	if recorded > 0 {
		oldest = l.items[0].Seq
	}
	if since > 0 && recorded > 0 && since < oldest {
		truncated = true
	}
	for _, e := range l.items {
		if e.Seq > since {
			events = append(events, e)
		}
	}
	if events == nil {
		events = []Event{}
	}
	return events, recorded, oldest, truncated
}

func (l *EventLog) Counts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for _, e := range l.items {
		out["total"]++
		if e.Source != "" {
			out["source_"+e.Source]++
		}
		if e.Apply != "" {
			out["apply_"+e.Apply]++
		}
		out["jev_calls"] += e.JevCalls
		out["jev_cached"] += e.JevCached
		out["jev_failed"] += e.JevFailed
		if e.ConnectStatus != "" {
			out["connect_"+e.ConnectStatus]++
		}
	}
	return out
}

func clipEvent(s string) string {
	if len(s) <= eventStrMax {
		return s
	}
	return s[:eventStrMax]
}
