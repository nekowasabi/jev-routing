package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

const usageParseLimit = 1 << 20 // 1MiB

// NormalizedUsage is protocol-normalized token usage. Missing fields stay nil.
type NormalizedUsage struct {
	InputTokens      *int   `json:"inputTokens"`
	OutputTokens     *int   `json:"outputTokens"`
	CachedTokens     *int   `json:"cachedTokens"`
	CacheWriteTokens *int   `json:"cacheWriteTokens"`
	ReasoningTokens  *int   `json:"reasoningTokens"`
	Source           string `json:"source,omitempty"`
}

type usageCollector struct {
	mu       sync.Mutex
	usage    *NormalizedUsage
	partial  bool
	missing  string
	jsonBuf  []byte
	sseBuf   []byte
	eventBuf []byte
	isSSE    bool
	sseHint  bool
	jsonDone bool
	limitHit bool
	connect  bool
	connBuf  []byte
	connErr  string
	finish   string
}

func newUsageCollector(contentType string) *usageCollector {
	ct := strings.ToLower(contentType)
	return &usageCollector{
		sseHint: strings.Contains(ct, "text/event-stream"),
		isSSE:   strings.Contains(ct, "text/event-stream"),
		connect: strings.Contains(ct, "connect"),
	}
}

func (c *usageCollector) Write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connect {
		if len(c.connBuf)+len(p) > usageParseLimit {
			if !c.limitHit {
				c.limitHit = true
				c.partial = true
				c.missing = "connect_limit"
			}
			return
		}
		c.connBuf = append(c.connBuf, p...)
		return
	}
	if c.isSSE || c.sseHint || looksSSE(p) {
		c.isSSE = true
		c.feedSSE(p)
		return
	}
	if !c.jsonDone {
		c.feedJSON(p)
	}
}

func looksSSE(p []byte) bool {
	s := string(p)
	return strings.Contains(s, "data:") || strings.HasPrefix(s, "event:")
}

func (c *usageCollector) feedJSON(p []byte) {
	if c.limitHit {
		return
	}
	if len(c.jsonBuf)+len(p) > usageParseLimit {
		c.limitHit = true
		c.partial = true
		c.missing = "json_limit"
		return
	}
	c.jsonBuf = append(c.jsonBuf, p...)
}

func (c *usageCollector) feedSSE(p []byte) {
	if c.limitHit {
		return
	}
	c.sseBuf = append(c.sseBuf, p...)
	for {
		n := indexNewLine(c.sseBuf)
		if n < 0 {
			if len(c.sseBuf) > usageParseLimit {
				c.limitHit = true
				c.partial = true
				c.missing = "sse_limit"
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
			c.flushSSEEvent()
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(line[5:])
			if len(c.eventBuf)+len(data)+1 > usageParseLimit {
				c.limitHit = true
				c.partial = true
				c.missing = "sse_event_limit"
				c.eventBuf = nil
				continue
			}
			if len(c.eventBuf) > 0 {
				c.eventBuf = append(c.eventBuf, '\n')
			}
			c.eventBuf = append(c.eventBuf, data...)
		}
	}
}

func indexNewLine(b []byte) int {
	return bytes.IndexByte(b, '\n')
}

func (c *usageCollector) flushSSEEvent() {
	if len(c.eventBuf) == 0 {
		return
	}
	if bytes.Equal(c.eventBuf, []byte("[DONE]")) {
		c.eventBuf = nil
		return
	}
	var obj map[string]any
	if json.Unmarshal(c.eventBuf, &obj) == nil {
		if u := extractUsage(obj); u != nil {
			// Last cumulative usage wins; do not sum repeats.
			c.usage = u
		}
	}
	c.eventBuf = nil
}

// finish returns usage, partial, missing, and a protocol-level finish hint
// ("" | "error" | "incomplete") for streams that carry their own status.
func (c *usageCollector) Finish() (*NormalizedUsage, bool, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connect {
		c.finishConnect()
		return c.usage, c.partial, c.missing, c.finish
	}
	if c.isSSE {
		if len(c.sseBuf) > 0 {
			c.feedSSE([]byte("\n\n"))
		}
		c.flushSSEEvent()
	} else if !c.jsonDone && len(c.jsonBuf) > 0 {
		var obj map[string]any
		if json.Unmarshal(c.jsonBuf, &obj) == nil {
			c.usage = extractUsage(obj)
			if c.usage == nil {
				c.missing = "no_usage"
			}
		} else {
			c.missing = "invalid_json"
		}
		c.jsonDone = true
	}
	if c.usage == nil && c.missing == "" {
		c.missing = "no_usage"
	}
	return c.usage, c.partial, c.missing, ""
}

// finishConnect scans Connect envelopes: end-stream trailers carry the
// protocol result, message frames may embed usage JSON.
func (c *usageCollector) finishConnect() {
	i := 0
	frames := 0
	for i+5 <= len(c.connBuf) {
		flags := c.connBuf[i]
		ln := int(binary.BigEndian.Uint32(c.connBuf[i+1 : i+5]))
		if i+5+ln > len(c.connBuf) {
			break
		}
		payload := c.connBuf[i+5 : i+5+ln]
		i += 5 + ln
		frames++
		if flags&connectFlagEndStream != 0 {
			var obj map[string]any
			if json.Unmarshal(payload, &obj) != nil {
				continue
			}
			if u := extractUsage(obj); u != nil {
				c.usage = u
			}
			if errObj, ok := obj["error"].(map[string]any); ok && errObj != nil {
				code, _ := errObj["code"].(string)
				msg, _ := errObj["message"].(string)
				c.connErr = clipEvent(strings.TrimSpace(code + " " + msg))
			}
			continue
		}
		raw := payload
		if flags&connectFlagCompressed != 0 {
			gr, err := gzip.NewReader(bytes.NewReader(raw))
			if err == nil {
				if dec, derr := io.ReadAll(gr); derr == nil {
					raw = dec
				}
				_ = gr.Close()
			}
		}
		for _, pj := range collectProtoJSON(raw) {
			if u := extractUsage(pj.obj); u != nil {
				c.usage = u
			}
		}
	}
	switch {
	case c.connErr != "":
		c.finish = "error"
	case i < len(c.connBuf):
		c.finish = "incomplete"
	}
	if c.usage == nil && c.missing == "" {
		if frames == 0 && len(c.connBuf) > 0 {
			c.missing = "connect_no_frames"
		} else {
			c.missing = "no_usage"
		}
	}
}

func extractUsage(obj map[string]any) *NormalizedUsage {
	raw, ok := obj["usage"]
	if !ok {
		// Anthropic message_delta / message_start, or a Responses API
		// response.completed event whose usage sits on response.
		for _, key := range []string{"message", "delta", "response"} {
			if nested, ok := obj[key].(map[string]any); ok {
				if raw, ok = nested["usage"]; ok {
					break
				}
			}
		}
		if raw == nil {
			return nil
		}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	u := &NormalizedUsage{Source: "upstream"}
	u.InputTokens = intField(m, "prompt_tokens", "input_tokens", "inputTokens")
	u.OutputTokens = intField(m, "completion_tokens", "output_tokens", "outputTokens")
	u.CachedTokens = intField(m, "cached_tokens", "cache_read_input_tokens", "cacheReadTokens")
	u.CacheWriteTokens = intField(m, "cache_creation_input_tokens", "cache_write_input_tokens", "cacheWriteTokens")
	u.ReasoningTokens = intField(m, "reasoning_tokens", "reasoningTokens")
	if details, ok := m["prompt_tokens_details"].(map[string]any); ok {
		if v := intField(details, "cached_tokens"); v != nil {
			u.CachedTokens = v
		}
	}
	if details, ok := m["input_tokens_details"].(map[string]any); ok {
		if v := intField(details, "cached_tokens"); v != nil {
			u.CachedTokens = v
		}
	}
	if details, ok := m["completion_tokens_details"].(map[string]any); ok {
		if v := intField(details, "reasoning_tokens"); v != nil {
			u.ReasoningTokens = v
		}
	}
	if details, ok := m["output_tokens_details"].(map[string]any); ok {
		if v := intField(details, "reasoning_tokens"); v != nil {
			u.ReasoningTokens = v
		}
	}
	if u.InputTokens == nil && u.OutputTokens == nil && u.CachedTokens == nil && u.ReasoningTokens == nil && u.CacheWriteTokens == nil {
		return nil
	}
	return u
}

func intField(m map[string]any, keys ...string) *int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				i := int(n)
				return &i
			case int:
				return &n
			case json.Number:
				i, err := n.Int64()
				if err == nil {
					ii := int(i)
					return &ii
				}
			}
		}
	}
	return nil
}

type usageReadCloser struct {
	rc     io.ReadCloser
	col    *usageCollector
	once   sync.Once
	onDone func(*NormalizedUsage, bool, string, string)
}

func wrapUsage(rc io.ReadCloser, contentType string, onDone func(*NormalizedUsage, bool, string, string)) io.ReadCloser {
	if rc == nil {
		return rc
	}
	return &usageReadCloser{rc: rc, col: newUsageCollector(contentType), onDone: onDone}
}

func (w *usageReadCloser) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if n > 0 {
		w.col.Write(p[:n])
	}
	if err == io.EOF {
		w.finish()
	}
	return n, err
}

func (w *usageReadCloser) Close() error {
	w.finish()
	return w.rc.Close()
}

func (w *usageReadCloser) finish() {
	w.once.Do(func() {
		u, partial, missing, finish := w.col.Finish()
		if w.onDone != nil {
			w.onDone(u, partial, missing, finish)
		}
	})
}
