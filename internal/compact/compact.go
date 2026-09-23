// Package compact ports tamaratran/fast-jev-compaction into the harness.
//
// User and assistant text stay verbatim. Only tool calls and tool results are
// scored. keepResult → keep both; keepCall only → truncate the result;
// otherwise drop both. First item and the newest preserveRecent items are pinned.
package compact

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
)

const (
	DefaultKeepThreshold    = 0.5
	DefaultPreserveRecent   = 6
	DefaultTruncateHead     = 300
	DefaultMaxStateTokens   = 25_000
	DefaultMaxRequestTokens = 30_000
	previewChars            = 200
	requestOverheadTokens   = 20
)

type Kind string

const (
	KindText   Kind = "text"
	KindCall   Kind = "tool_call"
	KindResult Kind = "tool_result"
	KindSum    Kind = "summary"
)

type Action string

const (
	ActionKeep     Action = "keep"
	ActionTruncate Action = "truncate"
	ActionDrop     Action = "drop"
)

type Item struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Chars   int    `json:"chars"`
	PairID  string `json:"pairId,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
	Body    string `json:"-"`
}

type Decision struct {
	ID     string `json:"id"`
	Action Action `json:"action"`
	Pinned bool   `json:"pinned"`
	Tool   string `json:"tool,omitempty"`
}

type Stats struct {
	CharsBefore  int    `json:"charsBefore"`
	CharsAfter   int    `json:"charsAfter"`
	CharsDropped int    `json:"charsDropped"`
	Kept         int    `json:"kept"`
	Truncated    int    `json:"truncated"`
	Dropped      int    `json:"dropped"`
	Pinned       int    `json:"pinned"`
	Items        int    `json:"items"`
	StateTokens  int    `json:"stateTokens"`
	StateStage   string `json:"stateStage"`
	Requests     int    `json:"requests"`
}

type Result struct {
	Decisions []Decision `json:"decisions"`
	Items     []Item     `json:"items"`
	Stats     Stats      `json:"stats"`
}

type Options struct {
	Goal              string
	KeepThreshold     float64
	PreserveRecent    int
	TruncateHeadChars int
	MaxStateTokens    int
	MaxRequestTokens  int
}

func Resolve(o Options) Options {
	if o.KeepThreshold == 0 {
		o.KeepThreshold = DefaultKeepThreshold
	}
	if o.PreserveRecent == 0 {
		o.PreserveRecent = DefaultPreserveRecent
	}
	if o.TruncateHeadChars == 0 {
		o.TruncateHeadChars = DefaultTruncateHead
	}
	if o.MaxStateTokens == 0 {
		o.MaxStateTokens = DefaultMaxStateTokens
	}
	if o.MaxRequestTokens == 0 {
		o.MaxRequestTokens = DefaultMaxRequestTokens
	}
	if o.PreserveRecent < 0 {
		o.PreserveRecent = 0
	}
	return o
}

type Candidate struct {
	Items  []Item
	Pinned bool
}

type NoulQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

func QuestionsFor(c Candidate) map[string]NoulQuestion {
	q := map[string]NoulQuestion{}
	for _, item := range c.Items {
		switch item.Kind {
		case KindCall:
			q["call_"+item.ID] = NoulQuestion{
				Type: "noul",
				Instructions: fmt.Sprintf(
					"Tool call %s (%s) should stay in the history: knowing this call was made, with its input, still matters for what the assistant does next",
					item.ID, item.Tool,
				),
			}
		case KindSum:
			q["summary_"+item.ID] = NoulQuestion{Type: "noul", Instructions: "Keep summary " + item.ID + "."}
		default:
			q["result_"+item.ID] = NoulQuestion{
				Type: "noul",
				Instructions: fmt.Sprintf(
					"The full output of tool call %s (%s, %d chars) should stay in the history verbatim: the assistant still needs its contents and re-running the tool would not do",
					item.ID, item.Tool, item.Chars,
				),
			}
		}
	}
	return q
}

func noulOf(answers map[string]float64, id string) (float64, bool) {
	if answers == nil {
		return 0, false
	}
	v, ok := answers[id]
	return v, ok
}

func DecideCall(c Candidate, answers map[string]float64, keepThreshold float64) []Decision {
	if c.Pinned {
		out := make([]Decision, 0, len(c.Items))
		for _, item := range c.Items {
			out = append(out, Decision{ID: item.ID, Action: ActionKeep, Pinned: true, Tool: item.Tool})
		}
		return out
	}
	if len(c.Items) == 2 {
		var call, result Item
		for _, item := range c.Items {
			if item.Kind == KindCall {
				call = item
			} else {
				result = item
			}
		}
		resultScore, resultOK := noulOf(answers, "result_"+result.ID)
		callScore, callOK := noulOf(answers, "call_"+call.ID)
		// Missing or invalid scores are not low scores: keep both so history is not dropped.
		if !resultOK || !callOK {
			return []Decision{
				{ID: call.ID, Action: ActionKeep, Tool: call.Tool},
				{ID: result.ID, Action: ActionKeep, Tool: result.Tool},
			}
		}
		keepResult := resultScore >= keepThreshold
		keepCall := callScore >= keepThreshold
		if keepResult {
			return []Decision{
				{ID: call.ID, Action: ActionKeep, Tool: call.Tool},
				{ID: result.ID, Action: ActionKeep, Tool: result.Tool},
			}
		}
		if keepCall {
			return []Decision{
				{ID: call.ID, Action: ActionKeep, Tool: call.Tool},
				{ID: result.ID, Action: ActionTruncate, Tool: result.Tool},
			}
		}
		return []Decision{
			{ID: call.ID, Action: ActionDrop, Tool: call.Tool},
			{ID: result.ID, Action: ActionDrop, Tool: result.Tool},
		}
	}
	item := c.Items[0]
	key := "result_" + item.ID
	if item.Kind == KindSum {
		key = "summary_" + item.ID
	}
	score, ok := noulOf(answers, key)
	if !ok {
		return []Decision{{ID: item.ID, Action: ActionKeep, Tool: item.Tool}}
	}
	if score >= keepThreshold {
		return []Decision{{ID: item.ID, Action: ActionKeep, Tool: item.Tool}}
	}
	if item.Kind == KindSum {
		return []Decision{{ID: item.ID, Action: ActionDrop, Tool: item.Tool}}
	}
	return []Decision{{ID: item.ID, Action: ActionTruncate, Tool: item.Tool}}
}

// KeepAll returns a result that retains every item. Used when external
// judgments are missing, invalid, or failed.
func KeepAll(items []Item, o Options) Result {
	o = Resolve(o)
	var decisions []Decision
	for _, item := range items {
		if item.Kind == KindText {
			continue
		}
		decisions = append(decisions, Decision{ID: item.ID, Action: ActionKeep, Tool: item.Tool})
	}
	applied := Apply(items, decisions, o.TruncateHeadChars)
	return Result{Decisions: decisions, Items: applied, Stats: Reduction(items, decisions, o.TruncateHeadChars)}
}

func charsAfter(item Item, action Action, head int) int {
	switch action {
	case ActionKeep:
		return item.Chars
	case ActionDrop:
		return 0
	default:
		if item.Chars < head {
			return item.Chars
		}
		return head
	}
}

func Reduction(items []Item, decisions []Decision, head int) Stats {
	byID := map[string]Decision{}
	for _, d := range decisions {
		byID[d.ID] = d
	}
	s := Stats{Items: len(items)}
	for _, item := range items {
		d, ok := byID[item.ID]
		if !ok {
			d = Decision{ID: item.ID, Action: ActionKeep}
		}
		s.CharsBefore += item.Chars
		s.CharsAfter += charsAfter(item, d.Action, head)
		switch d.Action {
		case ActionKeep:
			s.Kept++
		case ActionTruncate:
			s.Truncated++
		default:
			s.Dropped++
		}
		if d.Pinned {
			s.Pinned++
		}
	}
	s.CharsDropped = s.CharsBefore - s.CharsAfter
	return s
}

func isPinned(index, total, preserveRecent int) bool {
	return index == 0 || index >= total-preserveRecent
}

func CollectCandidates(items []Item, preserveRecent int) []Candidate {
	byPair := map[string][]int{}
	for i, item := range items {
		if item.PairID != "" && (item.Kind == KindCall || item.Kind == KindResult) {
			byPair[item.PairID] = append(byPair[item.PairID], i)
		}
	}
	valid := map[int][]int{}
	paired := map[int]bool{}
	for _, idxs := range byPair {
		var calls, results []int
		for _, i := range idxs {
			if items[i].Kind == KindCall {
				calls = append(calls, i)
			} else if items[i].Kind == KindResult {
				results = append(results, i)
			}
		}
		if len(calls) != 1 || len(results) != 1 {
			continue
		}
		min := idxs[0]
		for _, i := range idxs {
			if i < min {
				min = i
			}
		}
		valid[min] = idxs
		for _, i := range idxs {
			paired[i] = true
		}
	}
	pinAt := func(index int, item Item) bool {
		return item.Pinned || isPinned(index, len(items), preserveRecent)
	}
	var out []Candidate
	for i, item := range items {
		if pair, ok := valid[i]; ok {
			pairItems := make([]Item, 0, len(pair))
			pinned := false
			for _, j := range pair {
				pairItems = append(pairItems, items[j])
				if pinAt(j, items[j]) {
					pinned = true
				}
			}
			out = append(out, Candidate{Items: pairItems, Pinned: pinned})
			continue
		}
		if paired[i] || item.Kind == KindText || item.Kind == KindCall {
			continue
		}
		out = append(out, Candidate{Items: []Item{item}, Pinned: pinAt(i, item)})
	}
	return out
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var tokens float64
	var i int
	runes := []rune(text)
	for i < len(runes) {
		r := runes[i]
		switch {
		case unicode.IsDigit(r):
			j := i
			for j < len(runes) && unicode.IsDigit(runes[j]) {
				j++
			}
			tokens += float64(j-i) / 2
			i = j
		case unicode.IsLetter(r):
			j := i
			for j < len(runes) && unicode.IsLetter(runes[j]) {
				j++
			}
			tokens += 1 + math.Floor(float64(j-i-1)/6)
			i = j
		case unicode.IsSpace(r):
			i++
		default:
			tokens += 0.9
			i++
		}
	}
	n := int(math.Ceil(tokens))
	if n < 1 {
		return 1
	}
	return n
}

func Compact(items []Item, answers map[string]float64, o Options) Result {
	o = Resolve(o)
	cands := CollectCandidates(items, o.PreserveRecent)
	var decisions []Decision
	for _, c := range cands {
		decisions = append(decisions, DecideCall(c, answers, o.KeepThreshold)...)
	}
	applied := Apply(items, decisions, o.TruncateHeadChars)
	return Result{Decisions: decisions, Items: applied, Stats: Reduction(items, decisions, o.TruncateHeadChars)}
}

func TruncateBody(text string, isError bool, head int) string {
	if len(text) <= head+120 {
		return text
	}
	prefix := ""
	if head > 0 {
		prefix = text[:head] + "\n"
	}
	errNote := ""
	if isError {
		errNote = " (error)"
	}
	return fmt.Sprintf("%s[jev-compaction truncated %d chars of this tool result%s; re-run the tool if needed]",
		prefix, len(text)-head, errNote)
}

func Apply(items []Item, decisions []Decision, head int) []Item {
	byID := map[string]Decision{}
	for _, d := range decisions {
		byID[d.ID] = d
	}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		d, ok := byID[item.ID]
		if !ok || d.Action == ActionKeep {
			out = append(out, item)
			continue
		}
		if d.Action == ActionDrop {
			continue
		}
		body := TruncateBody(item.Body, false, head)
		if body == "" {
			body = TruncateBody(item.Preview, false, head)
		}
		item.Body = body
		item.Preview = clip(body, previewChars)
		item.Chars = len(body)
		out = append(out, item)
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// LocalAnswers scores keep-call / keep-result without a network round-trip.
// A later call of the same tool, or an edit of the same path, supersedes the result.
func LocalAnswers(items []Item, cands []Candidate) map[string]float64 {
	lastByTool := map[string]int{}
	for i, item := range items {
		if item.Tool != "" {
			lastByTool[item.Tool] = i
		}
	}
	indexOf := map[string]int{}
	for i, item := range items {
		indexOf[item.ID] = i
	}
	answers := map[string]float64{}
	for _, c := range cands {
		if c.Pinned {
			continue
		}
		for _, item := range c.Items {
			idx := indexOf[item.ID]
			superseded := false
			if item.Tool != "" && lastByTool[item.Tool] > idx {
				superseded = true
			}
			if strings.Contains(strings.ToLower(item.Preview+item.Tool), "read") || item.Kind == KindResult {
				for j := idx + 1; j < len(items); j++ {
					t := strings.ToLower(items[j].Tool)
					if strings.Contains(t, "edit") || strings.Contains(t, "search_replace") || strings.Contains(t, "apply_patch") || strings.Contains(t, "write") {
						superseded = true
						break
					}
				}
			}
			if item.Kind == KindCall {
				if superseded {
					answers["call_"+item.ID] = 0.72
				} else {
					answers["call_"+item.ID] = 0.88
				}
			} else {
				if superseded {
					answers["result_"+item.ID] = 0.12
					answers["summary_"+item.ID] = 0.12
				} else {
					answers["result_"+item.ID] = 0.86
					answers["summary_"+item.ID] = 0.86
				}
			}
		}
	}
	return answers
}

func CompactLocal(items []Item, o Options) Result {
	o = Resolve(o)
	cands := CollectCandidates(items, o.PreserveRecent)
	answers := LocalAnswers(items, cands)
	res := Compact(items, answers, o)
	res.Stats.Requests = 1
	res.Stats.StateStage = "local"
	if b, err := json.Marshal(items); err == nil {
		res.Stats.StateTokens = EstimateTokens(string(b))
	}
	return res
}

func ReductionRatio(r Result) float64 {
	if r.Stats.CharsBefore == 0 {
		return 0
	}
	return float64(r.Stats.CharsDropped) / float64(r.Stats.CharsBefore)
}
