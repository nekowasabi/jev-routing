package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// clear_tool_uses_20250919 is Anthropic's native context-editing strategy:
// https://platform.claude.com/docs/en/build-with-claude/context-editing
// It clears old tool_use/tool_result content server-side once a token
// trigger is hit, keeping the most recent N tool uses untouched.
const (
	clearToolUsesEditType   = "clear_tool_uses_20250919"
	clearToolUsesEditPrefix = "clear_tool_uses_"
	contextManagementBeta   = "context-management-2025-06-27"

	defaultClearTrigger = 100000
	defaultClearAtLeast = 20000
	defaultClearKeep    = 3
)

// clearToolUsesEdit builds the edit object from platform docs' trigger/keep/
// clear_at_least shape (all {"type": "...", "value": N}).
func clearToolUsesEdit(opt Options) map[string]any {
	edit := map[string]any{
		"type":           clearToolUsesEditType,
		"trigger":        map[string]any{"type": "input_tokens", "value": opt.ClaudeClearTrigger},
		"keep":           map[string]any{"type": "tool_uses", "value": opt.ClaudeClearKeep},
		"clear_at_least": map[string]any{"type": "input_tokens", "value": opt.ClaudeClearAtLeast},
	}
	// Why: only set exclude_tools when configured; an empty/omitted key keeps
	// the request byte-identical to before this option existed.
	if len(opt.ClaudeClearExclude) > 0 {
		names := make([]any, len(opt.ClaudeClearExclude))
		for i, n := range opt.ClaudeClearExclude {
			names[i] = n
		}
		edit["exclude_tools"] = names
	}
	return edit
}

// addClearToolUsesEdit appends our edit to root's context_management.edits,
// creating context_management if absent. It is a no-op (returns false) when
// a clear_tool_uses_* edit is already present or context_management exists
// but is not an object.
func addClearToolUsesEdit(root map[string]any, opt Options) bool {
	var cm map[string]any
	if raw, exists := root["context_management"]; exists {
		m, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		cm = m
	} else {
		cm = map[string]any{}
		root["context_management"] = cm
	}
	edits, _ := cm["edits"].([]any)
	for _, e := range edits {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); strings.HasPrefix(t, clearToolUsesEditPrefix) {
			return false
		}
	}
	cm["edits"] = append(edits, clearToolUsesEdit(opt))
	return true
}

// applyClaudeClearToolUses re-marshals raw with the clear_tool_uses edit
// appended and sets the beta header. Only tools[]/messages/system etc. are
// preserved as decoded; key order is not guaranteed byte-identical to raw
// (same trade-off RewriteWith already makes for every Changed=true path —
// Anthropic's prompt cache keys on cache_control-marked content blocks, not
// whole-body byte order, so this does not invalidate the cache).
func applyClaudeClearToolUses(raw []byte, header http.Header, opt Options) ([]byte, bool) {
	if !opt.ClaudeClearToolUses {
		return raw, false
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw, false
	}
	if !addClearToolUsesEdit(root, opt) {
		return raw, false
	}
	out, err := json.Marshal(root)
	if err != nil {
		return raw, false
	}
	addContextManagementBeta(header)
	return out, true
}

// addContextManagementBeta adds the beta flag to an existing comma-separated
// anthropic-beta header without disturbing other values (Claude Code already
// sends several, e.g. claude-code-20250219, oauth-2025-04-20).
func addContextManagementBeta(header http.Header) {
	existing := header.Get("anthropic-beta")
	if existing == "" {
		header.Set("anthropic-beta", contextManagementBeta)
		return
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.TrimSpace(part) == contextManagementBeta {
			return
		}
	}
	header.Set("anthropic-beta", existing+","+contextManagementBeta)
}

// extractClearEdits reads context_management.applied_edits from a response
// object (JSON body, or one SSE event's decoded payload) and sums the
// clear_tool_uses_* entries. found is false when the key is absent, which
// callers use to leave a prior value from an earlier SSE event untouched.
func extractClearEdits(obj map[string]any) (toolUses, inputTokens int, found bool) {
	cm, ok := obj["context_management"].(map[string]any)
	if !ok {
		return 0, 0, false
	}
	edits, ok := cm["applied_edits"].([]any)
	if !ok {
		return 0, 0, false
	}
	for _, e := range edits {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if !strings.HasPrefix(t, clearToolUsesEditPrefix) {
			continue
		}
		found = true
		toolUses += intAny(m["cleared_tool_uses"])
		inputTokens += intAny(m["cleared_input_tokens"])
	}
	return toolUses, inputTokens, found
}

func intAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return 0
}

// clearEditCollector mirrors usageCollector's JSON/SSE buffering (usage.go)
// to read applied_edits from the same response bodies, without disturbing
// usageCollector's tested Finish() signature.
type clearEditCollector struct {
	mu       sync.Mutex
	isSSE    bool
	jsonBuf  []byte
	sseBuf   []byte
	eventBuf []byte
	jsonDone bool
	limitHit bool
	toolUses int
	inputTok int
	found    bool
}

func newClearEditCollector(contentType string) *clearEditCollector {
	return &clearEditCollector{isSSE: strings.Contains(strings.ToLower(contentType), "text/event-stream")}
}

func (c *clearEditCollector) Write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isSSE || looksSSE(p) {
		c.isSSE = true
		c.feedSSE(p)
		return
	}
	if c.jsonDone || c.limitHit {
		return
	}
	if len(c.jsonBuf)+len(p) > usageParseLimit {
		c.limitHit = true
		return
	}
	c.jsonBuf = append(c.jsonBuf, p...)
}

func (c *clearEditCollector) feedSSE(p []byte) {
	if c.limitHit {
		return
	}
	c.sseBuf = append(c.sseBuf, p...)
	for {
		n := indexNewLine(c.sseBuf)
		if n < 0 {
			if len(c.sseBuf) > usageParseLimit {
				c.limitHit = true
				c.sseBuf = nil
			}
			return
		}
		line := c.sseBuf[:n]
		c.sseBuf = c.sseBuf[n+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			c.flushEvent()
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(line[5:])
			if len(c.eventBuf) > 0 {
				c.eventBuf = append(c.eventBuf, '\n')
			}
			c.eventBuf = append(c.eventBuf, data...)
		}
	}
}

func (c *clearEditCollector) flushEvent() {
	if len(c.eventBuf) == 0 {
		return
	}
	var obj map[string]any
	if json.Unmarshal(c.eventBuf, &obj) == nil {
		if tu, it, ok := extractClearEdits(obj); ok {
			c.toolUses, c.inputTok, c.found = tu, it, true
		}
	}
	c.eventBuf = nil
}

// Finish returns the summed clear_tool_uses_* values seen. For SSE, the
// last event carrying applied_edits wins (mirrors usageCollector's
// "last cumulative usage wins" contract for message_delta events).
func (c *clearEditCollector) Finish() (toolUses, inputTokens int, found bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isSSE {
		if len(c.sseBuf) > 0 {
			c.feedSSE([]byte("\n\n"))
		}
		c.flushEvent()
	} else if !c.jsonDone && len(c.jsonBuf) > 0 {
		var obj map[string]any
		if json.Unmarshal(c.jsonBuf, &obj) == nil {
			if tu, it, ok := extractClearEdits(obj); ok {
				c.toolUses, c.inputTok, c.found = tu, it, true
			}
		}
		c.jsonDone = true
	}
	return c.toolUses, c.inputTok, c.found
}

type clearEditReadCloser struct {
	rc     io.ReadCloser
	col    *clearEditCollector
	once   sync.Once
	onDone func(toolUses, inputTokens int, found bool)
}

func wrapClearEdits(rc io.ReadCloser, contentType string, onDone func(toolUses, inputTokens int, found bool)) io.ReadCloser {
	if rc == nil {
		return rc
	}
	return &clearEditReadCloser{rc: rc, col: newClearEditCollector(contentType), onDone: onDone}
}

func (w *clearEditReadCloser) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if n > 0 {
		w.col.Write(p[:n])
	}
	if err == io.EOF {
		w.finish()
	}
	return n, err
}

func (w *clearEditReadCloser) Close() error {
	w.finish()
	return w.rc.Close()
}

func (w *clearEditReadCloser) finish() {
	w.once.Do(func() {
		tu, it, ok := w.col.Finish()
		if w.onDone != nil {
			w.onDone(tu, it, ok)
		}
	})
}
