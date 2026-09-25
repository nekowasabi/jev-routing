package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/jev"
)

// codexSteerRetryCtxKey/codexSteerRetry stash the exact original request
// bytes (and the decided mode) on the outgoing request's context, so
// proxy.ModifyResponse can resend them once if upstream rejects the
// rewritten request with 400/422. See rewriteCodexSteer and the Handler
// wiring in proxy.go.
type codexSteerRetryCtxKey struct{}

type codexSteerRetry struct {
	original []byte
	mode     string // "forced" | "none"
	seq      int64
}

// resendCodexOriginal replays the exact request that was sent upstream,
// with its body swapped back to original. It reuses the outgoing request's
// already-resolved URL and headers (set by the reverse proxy's Director),
// so it needs no knowledge of the upstream's own URL-joining rules.
func (s *Server) resendCodexOriginal(outgoing *http.Request, original []byte) (*http.Response, error) {
	req := outgoing.Clone(outgoing.Context())
	req.Body = io.NopCloser(bytes.NewReader(original))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(original)), nil }
	req.ContentLength = int64(len(original))
	req.Header.Set("Content-Length", strconv.Itoa(len(original)))
	transport := http.DefaultTransport
	return transport.RoundTrip(req)
}

// codex_steer.go ports jev-gateway's Codex (Responses API) tool-steering
// path (adapters/responses.js, decide.js, questions.js, state.js) into Go,
// gated by Options.CodexSteer (JEV_CODEX_STEER). It answers two questions in
// one Jev call -- which tool (if any) should the assistant call next, and
// whether a tool is needed at all -- and, when both agree confidently on a
// forceable candidate, adds only `tool_choice` to the request. Everything
// else in the request body is left untouched. See docs/MEMO.md and the
// upstream jev-gateway source for the behavior this mirrors.
//
// Intentionally NOT ported (see task spec): `direct` (a synthesized
// Responses reply), `hint`, the >120-candidates two-stage shortlist, and
// decompression of a compacted request body.

const (
	// codexDefaultNamespace is the namespace whose tools are addressed by
	// bare name, matching adapters/responses.js DEFAULT_NAMESPACE.
	codexDefaultNamespace = "functions"

	codexToolKey      = "tool"
	codexNeedsToolKey = "needs_tool"
	// codexNoTool is the Choice label meaning "reply in text, call nothing",
	// matching decide.js NO_TOOL.
	codexNoTool = "no_tool_needed"

	// codexMinConfidence mirrors decide.js's config.minConfidence threshold.
	codexMinConfidence = 0.7

	// codexStateMaxMessageChars/MaxStateChars mirror state.js's
	// maxMessageChars/maxStateChars limits exactly (per-message clip and
	// total conversation budget, in characters).
	codexStateMaxMessageChars = 4000
	codexStateMaxChars        = 60000

	// codexMaxDescriptionChars/QuestionCharBudget mirror questions.js.
	codexMaxDescriptionChars  = 1024
	codexQuestionCharBudget   = 48_000
	codexDisagreeWantsToolMin = 0.3
	codexDisagreeNoToolMax    = 0.7

	// applyCodexNone marks a codex-steer decision that set tool_choice:"none".
	// Distinct from applyNone (the generic "nothing applied" default) so
	// dashboards and bench comparisons can tell the two apart.
	applyCodexNone = "codex_none"
)

// Passthrough reasons specific to the codex-steer path. Where jev-gateway's
// decide.js/questions.js use a specific literal string, it is reproduced
// here exactly so bench/dashboard consumers can match it.
const (
	reasonCodexNoTools        = "no_tools"
	reasonCodexNoMessages     = "no_messages"
	reasonCodexDuplicateNames = "duplicate_tool_names"
	reasonCodexToolChoiceSet  = "tool_choice_already_decided"
	// reasonCodexAgentMessage is not a jev-gateway literal (see the task
	// spec's own note); it is this port's own conservative addition for
	// conversation items this code cannot safely model.
	reasonCodexAgentMessage    = "agent_message"
	reasonCodexLowConfidence   = "low_confidence"
	reasonCodexAnswersDisagree = "jev_answers_disagree"
	reasonCodexHostedSelected  = "hosted_tool_selected"
	reasonCodexNamespaced      = "namespaced_tool_selected"
	reasonCodexUnexpectedJev   = "jev_unexpected_answer"
	reasonCodexUnknownTool     = "jev_unknown_tool"
)

// codexHostedDescriptions mirrors adapters/responses.js HOSTED_DESCRIPTIONS.
var codexHostedDescriptions = map[string]string{
	"web_search":         "Search the web for up-to-date information the assistant does not already have.",
	"web_search_preview": "Search the web for up-to-date information the assistant does not already have.",
	"local_shell":        "Run a shell command on the user's machine.",
	"image_generation":   "Generate an image.",
	"code_interpreter":   "Run Python code in a sandbox.",
	"file_search":        "Search the user's uploaded files.",
}

// codexCandidate is one tool jev-gateway's toTools() would have produced.
type codexCandidate struct {
	Kind        string // "function" | "custom" | "hosted"
	Name        string // fully qualified (namespace.child) when namespaced
	Description string
	Parameters  map[string]any // function only
	Namespaced  bool
}

type codexTurn struct {
	Role      string
	Text      string
	ToolCalls []codexToolCall
	Tool      string
	Content   string
}

type codexToolCall struct {
	Tool      string
	Arguments string
}

// MarshalJSON mirrors the turn shapes state.js/adapters/responses.js send to
// Jev: {role, text}, {role:"assistant", tool_calls:[...]}, or
// {role:"tool_result", tool, content}.
func (t codexTurn) toJSON() map[string]any {
	m := map[string]any{"role": t.Role}
	if t.Text != "" {
		m["text"] = t.Text
	}
	if len(t.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(t.ToolCalls))
		for _, c := range t.ToolCalls {
			calls = append(calls, map[string]any{"tool": c.Tool, "arguments": c.Arguments})
		}
		m["tool_calls"] = calls
	}
	if t.Role == "tool_result" {
		m["tool"] = t.Tool
		m["content"] = t.Content
	}
	return m
}

// strOr returns s, or fallback when s is empty.
func strOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// codexQualifiedName mirrors adapters/responses.js `qualified`.
func codexQualifiedName(namespace, name string) string {
	if namespace != "" && namespace != codexDefaultNamespace {
		return namespace + "." + name
	}
	return name
}

// codexInputItems mirrors adapters/responses.js `inputItems`.
func codexInputItems(root map[string]any) []any {
	switch v := root["input"].(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": v}}
	case []any:
		return v
	default:
		return nil
	}
}

// codexDeclaredTools mirrors adapters/responses.js `declaredTools`: the
// union of `tools` and any `additional_tools` input items.
func codexDeclaredTools(root map[string]any) []any {
	var declared []any
	if raw, ok := root["tools"].([]any); ok {
		declared = append(declared, raw...)
	}
	for _, item := range codexInputItems(root) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if str(m["type"]) != "additional_tools" {
			continue
		}
		if tools, ok := m["tools"].([]any); ok {
			declared = append(declared, tools...)
		}
	}
	return declared
}

// codexToolsFrom mirrors adapters/responses.js `toTools`: namespace tools
// are expanded (fully-qualified name = namespace.name + "." + child name),
// function/custom tools keep their type, and hosted tools count once per
// type. duplicate reports a name collision (jev-gateway's Map.set would
// silently keep the last write; decide.js's skipReason treats that
// possibility as a passthrough trigger for the caller).
func codexToolsFrom(raw []any) (candidates []codexCandidate, duplicate bool) {
	order := []string{}
	byName := map[string]codexCandidate{}
	var add func(tool map[string]any, namespace map[string]any)
	add = func(tool map[string]any, namespace map[string]any) {
		typ := str(tool["type"])
		if typ == "namespace" {
			nested, _ := tool["tools"].([]any)
			for _, n := range nested {
				if nm, ok := n.(map[string]any); ok {
					add(nm, tool)
				}
			}
			return
		}
		name := str(tool["name"])
		if (typ == "function" || typ == "custom") && name != "" {
			nsName := ""
			if namespace != nil {
				nsName = str(namespace["name"])
			}
			qualified := codexQualifiedName(nsName, name)
			desc, _ := tool["description"].(string)
			namespaced := qualified != name
			if namespaced {
				if group, ok := namespace["description"].(string); ok {
					group = strings.TrimSpace(group)
					if group != "" {
						if desc != "" {
							desc = "[" + group + "] " + desc
						} else {
							desc = "[" + group + "]"
						}
					}
				}
			}
			var params map[string]any
			if typ == "function" {
				params, _ = tool["parameters"].(map[string]any)
			}
			if _, seen := byName[qualified]; seen {
				duplicate = true
			} else {
				order = append(order, qualified)
			}
			byName[qualified] = codexCandidate{Kind: typ, Name: qualified, Description: desc, Parameters: params, Namespaced: namespaced}
			return
		}
		if typ != "" {
			if _, seen := byName[typ]; !seen {
				desc := codexHostedDescriptions[typ]
				if desc == "" {
					if d, ok := tool["description"].(string); ok && d != "" {
						desc = d
					} else {
						desc = "The built-in " + typ + " tool."
					}
				}
				order = append(order, typ)
				byName[typ] = codexCandidate{Kind: "hosted", Name: typ, Description: desc}
			}
		}
	}
	for _, t := range raw {
		if m, ok := t.(map[string]any); ok {
			add(m, nil)
		}
	}
	candidates = make([]codexCandidate, 0, len(order))
	for _, name := range order {
		candidates = append(candidates, byName[name])
	}
	return candidates, duplicate
}

// codexTurnsFrom mirrors adapters/responses.js `toInput`'s item loop:
// system/developer messages accumulate into `system`, everything else
// becomes a turn. hasAgentMessage reports whether any item has type
// "agent_message" (sub-agent related; this port passes those requests
// through untouched rather than modeling them -- see the file header).
func codexTurnsFrom(root map[string]any) (system string, turns []codexTurn, hasAgentMessage bool) {
	var systemParts []string
	if instr, ok := root["instructions"].(string); ok && instr != "" {
		systemParts = append(systemParts, instr)
	}
	toolNameByCallID := map[string]string{}
	for _, item := range codexInputItems(root) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ := str(m["type"])
		if typ == "" && m["role"] != nil {
			typ = "message"
		}
		switch {
		case typ == "agent_message":
			hasAgentMessage = true
		case typ == "message":
			role := str(m["role"])
			text := truncateRunes(codexTextOf(m["content"]), codexStateMaxMessageChars)
			if role == "system" || role == "developer" {
				if text != "" {
					systemParts = append(systemParts, text)
				}
			} else {
				if role == "" {
					role = "user"
				}
				turns = append(turns, codexTurn{Role: role, Text: text})
			}
		case typ == "function_call" || typ == "custom_tool_call":
			name := codexQualifiedName(str(m["namespace"]), strOr(str(m["name"]), "unknown"))
			if callID := str(m["call_id"]); callID != "" {
				toolNameByCallID[callID] = name
			}
			args := m["arguments"]
			if args == nil {
				args = m["input"]
			}
			turns = append(turns, codexTurn{Role: "assistant", ToolCalls: []codexToolCall{
				{Tool: name, Arguments: truncateRunes(codexTextOf(args), codexStateMaxMessageChars)},
			}})
		case typ == "local_shell_call":
			if callID := str(m["call_id"]); callID != "" {
				toolNameByCallID[callID] = "local_shell"
			}
			var cmd []string
			if action, ok := m["action"].(map[string]any); ok {
				if parts, ok := action["command"].([]any); ok {
					for _, p := range parts {
						if s, ok := p.(string); ok {
							cmd = append(cmd, s)
						}
					}
				}
			}
			turns = append(turns, codexTurn{Role: "assistant", ToolCalls: []codexToolCall{
				{Tool: "local_shell", Arguments: truncateRunes(strings.Join(cmd, " "), codexStateMaxMessageChars)},
			}})
		case strings.HasSuffix(typ, "_call_output"):
			tool := toolNameByCallID[str(m["call_id"])]
			if tool == "" {
				tool = "unknown"
			}
			turns = append(turns, codexTurn{Role: "tool_result", Tool: tool, Content: truncateRunes(codexTextOf(m["output"]), codexStateMaxMessageChars)})
		}
		// Reasoning items (encrypted), item references, and hosted-tool
		// traces carry nothing Jev can read -- silently skipped, matching
		// adapters/responses.js.
	}
	system = truncateRunes(strings.Join(systemParts, "\n\n"), codexStateMaxMessageChars)
	return system, turns, hasAgentMessage
}

// codexTextOf mirrors state.js `textOf`: flattens content parts, or leaves
// a placeholder for anything else.
func codexTextOf(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, p := range v {
			if pm, ok := p.(map[string]any); ok {
				if t, ok := pm["text"].(string); ok {
					parts = append(parts, t)
					continue
				}
				typ := str(pm["type"])
				if typ == "" {
					typ = "attachment"
				}
				parts = append(parts, "["+typ+"]")
				continue
			}
			parts = append(parts, "[attachment]")
		}
		return strings.Join(parts, "\n")
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// truncateRunes mirrors state.js `truncate`: keeps the head (60%) and tail
// of long text, replacing the middle with a marker. Text is measured in
// runes rather than JS's UTF-16 code units -- a deliberate, documented
// deviation for Go string safety; see the task report.
func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	const marker = " …[truncated]… "
	markerLen := len([]rune(marker))
	keep := max - markerLen
	if keep < 0 {
		keep = 0
	}
	head := (keep*6 + 9) / 10 // ceil(keep * 0.6)
	tail := keep - head
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}

// codexBuildState mirrors state.js `buildState`: newest-first budget walk,
// final conversation array in chronological order.
func codexBuildState(system string, turns []codexTurn) map[string]any {
	budget := codexStateMaxChars - len(system)
	var kept []codexTurn
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		b, _ := json.Marshal(t.toJSON())
		budget -= len(b)
		if budget < 0 && len(kept) > 0 {
			break
		}
		kept = append([]codexTurn{t}, kept...)
	}
	omitted := len(turns) - len(kept)
	state := map[string]any{}
	if system != "" {
		state["assistant_instructions"] = system
	}
	if omitted > 0 {
		state["earlier_turns_omitted"] = omitted
	}
	conv := make([]map[string]any, 0, len(kept))
	for _, t := range kept {
		conv = append(conv, t.toJSON())
	}
	state["conversation"] = conv
	return state
}

// codexToolCriteria mirrors questions.js `toolCriteria`.
func codexToolCriteria(candidates []codexCandidate) map[string]string {
	n := len(candidates)
	if n == 0 {
		return map[string]string{}
	}
	limit := codexQuestionCharBudget / n
	if limit > codexMaxDescriptionChars {
		limit = codexMaxDescriptionChars
	}
	out := make(map[string]string, n)
	for _, c := range candidates {
		desc := strings.TrimSpace(c.Description)
		if len(desc) > limit {
			desc = string([]rune(desc)[:min(limit, len([]rune(desc)))])
		}
		if desc == "" && len(c.Parameters) > 0 {
			if props, ok := c.Parameters["properties"].(map[string]any); ok && len(props) > 0 {
				names := make([]string, 0, len(props))
				for k := range props {
					names = append(names, k)
				}
				desc = "Parameters: " + strings.Join(names, ", ")
			}
		}
		out[c.Name] = desc
	}
	return out
}

// codexFindCandidate returns the candidate matching name, if any.
func codexFindCandidate(candidates []codexCandidate, name string) (codexCandidate, bool) {
	for _, c := range candidates {
		if c.Name == name {
			return c, true
		}
	}
	return codexCandidate{}, false
}

// codexToolChoiceState classifies the caller's tool_choice, mirroring
// adapters/responses.js: "auto"/"required" pass through as-is, anything
// else (including an already-forced object) means the caller already
// decided.
func codexToolChoiceState(root map[string]any) (state string, decided bool) {
	v, ok := root["tool_choice"]
	if !ok || v == nil {
		return "auto", false
	}
	if s, ok := v.(string); ok {
		if s == "auto" || s == "required" {
			return s, false
		}
	}
	return "decided", true
}

// jsonTruthy mirrors JS truthiness for the previous_response_id check.
func jsonTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	default:
		return true
	}
}

// rewriteCodexSteer is the ported decide()+apply() pipeline. It mutates
// stats to record the decision using jev-routing's existing RewriteStats/
// Event field conventions, and returns the body to send upstream, whether a
// live Jev call was attempted, and its error (if any) -- both handed back
// to RewriteWith's existing recordDecision/ConnectStatus bookkeeping.
func rewriteCodexSteer(ctx context.Context, body []byte, root map[string]any, client *jev.Client, stats *RewriteStats) (out []byte, asked bool, callErr error) {
	passthrough := func(reason string) ([]byte, bool, error) {
		stats.Reason = reason
		stats.Chosen = "passthrough:" + reason
		return body, asked, callErr
	}

	if jsonTruthy(root["previous_response_id"]) {
		return passthrough(reasonPreviousResponse)
	}
	if _, decided := codexToolChoiceState(root); decided {
		return passthrough(reasonCodexToolChoiceSet)
	}

	candidates, duplicate := codexToolsFrom(codexDeclaredTools(root))
	if duplicate {
		return passthrough(reasonCodexDuplicateNames)
	}
	if len(candidates) == 0 {
		return passthrough(reasonCodexNoTools)
	}
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.Name)
	}
	stats.CandidateNames = names
	stats.CandidateCount = len(candidates)

	system, turns, hasAgentMessage := codexTurnsFrom(root)
	if hasAgentMessage {
		return passthrough(reasonCodexAgentMessage)
	}
	if len(turns) == 0 {
		return passthrough(reasonCodexNoMessages)
	}

	if client == nil || !client.Live() {
		return passthrough(reasonKeyMissing)
	}

	criteria := codexToolCriteria(candidates)
	toolChoiceState, _ := codexToolChoiceState(root)
	allowNone := toolChoiceState != "required"
	if allowNone {
		criteria[codexNoTool] = "No tool call is needed right now: the assistant should reply to the user in plain text " +
			"(answer directly, ask a clarifying question, or report results that tools already returned)."
	}
	questions := map[string]jev.Question{
		codexToolKey: {
			Type: "choice",
			Instructions: "Given the conversation, what should the assistant do next? " +
				"Pick the single tool whose call best advances the user's latest request.",
			Criteria: criteria,
		},
		codexNeedsToolKey: {
			Type:         "noul",
			Instructions: "Does the assistant need to call one of its tools now, rather than reply to the user in plain text?",
		},
	}
	state := codexBuildState(system, turns)

	asked = true
	res, err := client.AskSelectionContext(ctx, state, questions)
	if err != nil {
		callErr = err
		return passthrough(reasonCallFailed)
	}
	picked, pickedOK := jev.ParseChoice(res, codexToolKey)
	needs, needsOK := jev.ParseNoul(res, codexNeedsToolKey)
	if !pickedOK || !needsOK {
		return passthrough(reasonCodexUnexpectedJev)
	}
	stats.Source = sourceJev
	stats.Confidence = picked.Conf
	stats.NeedsTool = needs.Noul

	if picked.Conf < codexMinConfidence {
		return passthrough(reasonCodexLowConfidence)
	}
	wantsTool := picked.Choice != codexNoTool
	disagree := needs.Noul < codexDisagreeWantsToolMin
	if !wantsTool {
		disagree = needs.Noul > codexDisagreeNoToolMax
	}
	if disagree {
		return passthrough(reasonCodexAnswersDisagree)
	}

	if !wantsTool {
		stats.Apply = applyCodexNone
		stats.Chosen = picked.Choice
		root["tool_choice"] = "none"
		nb, merr := json.Marshal(root)
		if merr != nil {
			return passthrough(reasonCodexUnexpectedJev)
		}
		stats.Changed = true
		return nb, asked, callErr
	}

	cand, found := codexFindCandidate(candidates, picked.Choice)
	if !found {
		return passthrough(reasonCodexUnknownTool)
	}
	if cand.Kind == "hosted" {
		return passthrough(reasonCodexHostedSelected)
	}
	if cand.Namespaced {
		return passthrough(reasonCodexNamespaced)
	}

	kindStr := "function"
	if cand.Kind == "custom" {
		kindStr = "custom"
	}
	stats.Apply = applyForced
	stats.Chosen = picked.Choice
	stats.ForcedTool = picked.Choice
	root["tool_choice"] = map[string]any{"type": kindStr, "name": picked.Choice}
	nb, merr := json.Marshal(root)
	if merr != nil {
		return passthrough(reasonCodexUnexpectedJev)
	}
	stats.Changed = true
	return nb, asked, callErr
}
