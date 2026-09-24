package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func TestParseTransformsIndependent(t *testing.T) {
	got, err := parseTransforms("compact=off,filter=on,criteria=on")
	if err != nil || got.Compact || !got.Filter || !got.Criteria {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := parseTransforms("nope=on"); err == nil {
		t.Fatal("unknown transform must fail")
	}
	if _, err := parseTransforms("compact"); err == nil {
		t.Fatal("bare name must fail")
	}
}

func TestCriteriaForUnregisteredStaysRaw(t *testing.T) {
	if got := criteriaFor("grep", "Search files", true); got != "Search files" {
		t.Fatalf("unregistered must stay raw: %q", got)
	}
	if got := criteriaFor("grep", "Search files", false); got != "Search files" {
		t.Fatalf("disabled must stay raw: %q", got)
	}
}

func TestCriteriaForRegisteredPair(t *testing.T) {
	old := contrastCriteria
	contrastCriteria = map[string]contrastSpec{
		"grep": {Covers: "search text", NotFor: "read whole files", Examples: []string{"find TODO"}},
	}
	t.Cleanup(func() { contrastCriteria = old })

	got := criteriaFor("grep", "raw desc", true)
	if !strings.Contains(got, "covers: search text") || !strings.Contains(got, "not_for: read whole files") || !strings.Contains(got, "examples: find TODO") {
		t.Fatalf("registered pair: %q", got)
	}
	if criteriaFor("grep", "raw desc", false) != "raw desc" {
		t.Fatal("disabled must stay raw")
	}
	if criteriaFor("read", "raw", true) != "raw" {
		t.Fatal("unregistered neighbor must stay raw")
	}
}

func TestShortenCriteriaFirstParagraphAndByteLimit(t *testing.T) {
	desc := strings.Repeat("Do a thing carefully. ", 20) + "\n\nSecond paragraph with implementation detail Jev does not need."
	got := shortenCriteria(desc)
	if strings.Contains(got, "Second paragraph") {
		t.Fatalf("second paragraph must be dropped: %q", got)
	}
	if len(got) > criteriaByteLimit {
		t.Fatalf("must stay within %d bytes: %d bytes: %q", criteriaByteLimit, len(got), got)
	}
	if !strings.HasSuffix(got, ".") {
		t.Fatalf("must cut at a sentence end: %q", got)
	}

	noSentence := strings.Repeat("x", 400)
	got2 := shortenCriteria(noSentence)
	if !strings.HasSuffix(got2, "…") || len(got2) != criteriaByteLimit+len("…") {
		t.Fatalf("must fall back to a hard cut with an ellipsis: %d bytes: %q", len(got2), got2)
	}

	short := "Search files with a regex."
	if got := shortenCriteria(short); got != short {
		t.Fatalf("short single-paragraph description must stay unchanged: %q", got)
	}
}

func toolDescByName(root map[string]any, name string) string {
	for _, raw := range asSlice(root["tools"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			d, _ := m["description"].(string)
			return d
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if n, _ := fn["name"].(string); n == name {
				d, _ := fn["description"].(string)
				return d
			}
		}
	}
	return ""
}

// TestClaudeCriteriaShortenedNotCodexNorUpstream covers change 3: only
// Claude's Jev judgment criteria is shortened, Codex's is sent unchanged, and
// neither host's forwarded tool definition is altered.
func TestClaudeCriteriaShortenedNotCodexNorUpstream(t *testing.T) {
	longDesc := strings.Repeat("Run a shell command safely and capture its output. ", 12) +
		"\n\nSecond paragraph with implementation detail that Jev does not need to pick the tool."
	opt := steerOpt()
	opt.SelectionMode = SelectionJev

	captureCriteria := func(t *testing.T, choice string, got *string) *jev.Client {
		return jevAnswers(t, choice, 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Questions map[string]jev.Question `json:"questions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			*got = req.Questions["next_tool"].Criteria["Bash"]
			_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": choice, "confidence": 0.9, "probabilities": adoptTestProbs(choice)},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			}})
		})
	}

	// Claude: criteria sent to Jev is shortened; the forwarded body keeps the full description.
	var gotClaude string
	claudeClient := captureCriteria(t, "Read", &gotClaude)
	claudeReq := claudeAdviseReq(claudeToolTurn("toolu_1")...)
	claudeReq["tools"] = []any{
		map[string]any{"name": "Bash", "description": longDesc},
		map[string]any{"name": "Read", "description": "Read a file from disk."},
	}
	rawClaude, _ := json.Marshal(claudeReq)
	outClaude, statsClaude, err := RewriteWith(t.Context(), rawClaude, host.Claude, claudeClient, opt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotClaude, "Second paragraph") || len(gotClaude) > criteriaByteLimit {
		t.Fatalf("claude criteria must be shortened: %d bytes: %q", len(gotClaude), gotClaude)
	}
	if statsClaude.Apply != applyAdvise {
		t.Fatalf("claude advice must still apply: %+v", statsClaude)
	}
	var gotClaudeBody map[string]any
	if err := json.Unmarshal(outClaude, &gotClaudeBody); err != nil {
		t.Fatal(err)
	}
	if toolDescByName(gotClaudeBody, "Bash") != longDesc {
		t.Fatalf("claude upstream tool description changed: %q", toolDescByName(gotClaudeBody, "Bash"))
	}

	// Codex: same description reaches Jev and upstream unshortened.
	var gotCodex string
	codexClient := captureCriteria(t, "Bash", &gotCodex)
	rawCodex, _ := json.Marshal(map[string]any{
		"model": "gpt-5.6-terra",
		"messages": []any{
			map[string]any{"role": "user", "content": "run the build"},
		},
		"tools": []any{
			grokFn("Bash", longDesc),
			grokFn("Read", "Read a file from disk."),
		},
	})
	outCodex, _, err := RewriteWith(t.Context(), rawCodex, host.Codex, codexClient, opt)
	if err != nil {
		t.Fatal(err)
	}
	if gotCodex != longDesc {
		t.Fatalf("codex criteria must stay unchanged: %d bytes: %q", len(gotCodex), gotCodex)
	}
	var gotCodexBody map[string]any
	if err := json.Unmarshal(outCodex, &gotCodexBody); err != nil {
		t.Fatal(err)
	}
	if toolDescByName(gotCodexBody, "Bash") != longDesc {
		t.Fatalf("codex upstream tool description changed: %q", toolDescByName(gotCodexBody, "Bash"))
	}
}

func TestCompactTransformOffLeavesHistory(t *testing.T) {
	fat := strings.Repeat("line of grep output\n", 200)
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it and fix it in place."},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "grep", "arguments": `{"pattern":"auth"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "name": "grep", "content": fat},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"x"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c2", "name": "read_file", "content": "file body"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c3", "type": "function", "function": map[string]any{"name": "search_replace", "arguments": `{}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c3", "name": "search_replace", "content": "ok"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
		},
	}
	raw, _ := json.Marshal(req)
	opt := DefaultOptions()
	opt.Transforms.Compact = false
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CompactApplied || containsName(stats.Transforms, transformCompact) {
		t.Fatalf("compact=off must not rewrite history: %+v", stats)
	}
}

func TestFilterTransformOffLeavesCatalog(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := DefaultOptions()
	opt.Transforms.Filter = false
	out, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter != stats.ToolBefore || stats.Apply != applyNone {
		t.Fatalf("filter=off must leave the catalog: %+v", stats)
	}
	if containsName(stats.Transforms, transformFilter) {
		t.Fatalf("filter must not be recorded when disabled: %v", stats.Transforms)
	}
	if len(stats.ProposedKept) == 0 {
		t.Fatal("filter=off must still compute a proposed set")
	}
	if len(catalogNames(out)) != 6 {
		t.Fatalf("forwarded catalog changed: %v", catalogNames(out))
	}
}

func TestFilterOffPassesCodexRequestThroughWithoutJev(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"model": "gpt-5.6-terra",
		"input": []any{map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it."}},
		"tools": []any{
			map[string]any{"type": "function", "name": "shell"},
			map[string]any{"type": "function", "name": "read_file"},
			map[string]any{"type": "function", "name": "grep_files"},
			map[string]any{"type": "function", "name": "apply_patch"},
		},
	})
	var calls int64
	client := fakeNextToolClient(t, "grep_files", 0.1, &calls)
	off := DefaultOptions()
	off.Transforms.Filter = false
	out, stats, err := RewriteWith(t.Context(), raw, host.Codex, client, off)
	if err != nil || string(out) != string(raw) || stats.Changed {
		t.Fatalf("filter=off must forward the body unchanged: %+v err=%v\n%s", stats, err, out)
	}
	if stats.Chosen != "passthrough:"+reasonFilterOff || calls != 0 {
		t.Fatalf("filter=off must not ask Jev: chosen=%s calls=%d", stats.Chosen, calls)
	}
	opt := steerOpt()
	opt.SelectionMode = SelectionJev
	if _, _, err := RewriteWith(t.Context(), raw, host.Codex, client, opt); err != nil || calls == 0 {
		t.Fatalf("filter=on must reach Jev (guards the zero-call check): calls=%d err=%v", calls, err)
	}
}

func TestAppliedTransformsRecordedIndependently(t *testing.T) {
	raw := evalCatalogBody(
		"Do not parallel. Sequential search and read the definition of RewriteWith.",
		[]string{"read_file", "grep", "search_replace", "run_terminal_cmd", "web_search", "send_feedback"},
	)
	opt := steerOpt()
	_, stats, err := RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !containsName(stats.Transforms, transformFilter) {
		t.Fatalf("filter apply should record transform: %+v", stats)
	}
	if containsName(stats.Transforms, transformCriteria) {
		t.Fatalf("empty contrast map must not record criteria: %v", stats.Transforms)
	}

	opt.Shadow = true
	opt.Transforms.Criteria = true
	_, stats, err = RewriteWith(t.Context(), raw, host.Grok, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if containsName(stats.Transforms, transformFilter) {
		t.Fatalf("shadow must not record filter: %v", stats.Transforms)
	}
	if !containsName(stats.Transforms, transformCriteria) {
		t.Fatalf("criteria flag must be recorded when enabled: %v", stats.Transforms)
	}
	ev := EventFromStats(stats)
	if ev.Shadow != stats.Shadow || !containsName(ev.Transforms, transformCriteria) {
		t.Fatalf("event lost transform ids: %+v", ev)
	}
}
