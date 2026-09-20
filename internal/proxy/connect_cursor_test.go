package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func cursorAgentHistJSON(t *testing.T) []string {
	t.Helper()
	mustJSON := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return []string{
		mustJSON(map[string]any{"role": "user", "content": "FIND_THIS_PROMPT locate the failing auth test"}),
		mustJSON(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{"path": "SECRET_ARG"}}}}),
		mustJSON(map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "a", "content": strings.Repeat("SECRET_RESULT\n", 300)}}}),
		mustJSON(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "b", "name": "Read", "input": map[string]any{"path": "SECRET_ARG"}}}}),
		mustJSON(map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "b", "content": strings.Repeat("SECRET_RESULT\n", 300)}}}),
		mustJSON(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "c", "name": "Edit", "input": map[string]any{"path": "SECRET_ARG"}}}}),
		mustJSON(map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "c", "content": strings.Repeat("SECRET_RESULT\n", 300)}}}),
		mustJSON(map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."}),
	}
}

func testMcpTool(name, desc string) []byte {
	var b []byte
	b = append(b, protoString(1, name)...)
	b = append(b, protoString(2, desc)...)
	b = append(b, protoString(6, `{"type":"object","secret":"KEEP_SCHEMA_`+name+`"}`)...)
	return b
}

func testAgentRunRequest(t *testing.T) []byte {
	t.Helper()
	var cs []byte
	for _, s := range cursorAgentHistJSON(t) {
		cs = append(cs, protoString(1, s)...)
	}
	mcp := protoRepeated(1, [][]byte{
		testMcpTool("Read", "Read a file"),
		testMcpTool("Grep", "Search file contents"),
		testMcpTool("Shell", "Run a command"),
		testMcpTool("Write", "Write a file"),
	})
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, mcp)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)
	return req
}

func testAgentRunRequestIDsOnly(t *testing.T) []byte {
	t.Helper()
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)
	return req
}

func decodeConnectPayload(t *testing.T, frame []byte) []byte {
	t.Helper()
	if len(frame) < 5 {
		t.Fatalf("short frame %d", len(frame))
	}
	payload := frame[5:]
	if frame[0]&connectFlagCompressed == 0 {
		return payload
	}
	gr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gr)
	_ = gr.Close()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func protoFieldString(body []byte, field int) string {
	if raw := firstLD(body, field); raw != nil {
		return string(raw)
	}
	return ""
}

func countMcpTools(req []byte) int {
	mcp := firstLD(req, 4)
	if mcp == nil {
		return 0
	}
	fields, ok := parseProtoFields(mcp)
	if !ok {
		return 0
	}
	n := 0
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 {
			n++
		}
	}
	return n
}

func mcpToolRawByName(req []byte, name string) []byte {
	mcp := firstLD(req, 4)
	if mcp == nil {
		return nil
	}
	fields, ok := parseProtoFields(mcp)
	if !ok {
		return nil
	}
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		n, _ := mcpToolNameDesc(f.raw)
		if n == name {
			return f.raw
		}
	}
	return nil
}

func assertConnectCursorRewrite(t *testing.T, frame []byte, wrapped bool) ([]byte, RewriteStats) {
	t.Helper()
	out, stats, catalog, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("rewrite not applied")
	}
	if catalog == nil || (catalog.RawCount == 0 && len(catalog.HistoryTypes) == 0) {
		t.Fatalf("catalog missing history/tools: %+v", catalog)
	}
	if stats.Apply != applyFilter || !stats.Changed || stats.Chosen != "Grep" || stats.ToolAfter >= stats.ToolBefore {
		t.Fatalf("want Grep filter, got %+v", stats)
	}
	if !stats.CompactApplied {
		t.Fatalf("want compaction, got %+v", stats)
	}
	payload := decodeConnectPayload(t, out)
	req, wrapField, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("rewritten payload is not AgentRunRequest")
	}
	if (wrapField > 0) != wrapped {
		t.Fatalf("wrapField=%d wrapped=%v want %v", wrapField, wrapField > 0, wrapped)
	}
	if protoFieldString(req, 5) != "conv-agent-1" || protoFieldString(req, 13) != "cursor" || protoFieldString(req, 26) != "sess-keep" {
		t.Fatalf("ids dropped id=%s harness=%s session=%s", protoFieldString(req, 5), protoFieldString(req, 13), protoFieldString(req, 26))
	}
	if protoFieldString(req, 25) != "run-id-keep" {
		t.Fatalf("unknown field 25 dropped: %q", protoFieldString(req, 25))
	}
	after := countMcpTools(req)
	if after == 0 || after >= 4 {
		t.Fatalf("tools after=%d", after)
	}
	grep := mcpToolRawByName(req, "Grep")
	if grep == nil || !bytes.Contains(grep, []byte("KEEP_SCHEMA_Grep")) {
		t.Fatal("kept tool proto bytes were re-encoded")
	}
	cs := firstLD(req, 1)
	if cs == nil {
		t.Fatal("conversation_state missing")
	}
	fields, ok := parseProtoFields(cs)
	if !ok {
		t.Fatal("conversation_state not proto")
	}
	hist := 0
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		hist++
		if !json.Valid(f.raw) {
			t.Fatalf("history element not JSON string: %q", f.raw)
		}
	}
	if hist == 0 {
		t.Fatal("history left root_prompt_messages_json")
	}
	encoded, _ := json.Marshal(stats)
	for _, leak := range []string{"FIND_THIS_PROMPT", "SECRET_ARG", "SECRET_RESULT"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("stats leaked %s: %s", leak, encoded)
		}
	}
	return req, stats
}

func catalogKeys(c *CatalogShape) []string {
	if c == nil {
		return nil
	}
	return c.Keys
}

func catalogHasToolsOrP4(c *CatalogShape) bool {
	if c == nil {
		return false
	}
	for _, k := range c.Keys {
		if k == "mcpTools" || k == "tools" || k == "p4" || strings.HasPrefix(k, "p4") {
			return true
		}
	}
	return false
}

func TestRewriteConnectCursorFirstFrameFiltersAndCompacts(t *testing.T) {
	frame := connectFrame(0, protoBytes(1, testAgentRunRequest(t)))
	assertConnectCursorRewrite(t, frame, true)
}

func TestRewriteConnectCursorBareAgentRunRequest(t *testing.T) {
	frame := connectFrame(0, testAgentRunRequest(t))
	assertConnectCursorRewrite(t, frame, false)
}

func TestRewriteConnectCursorGzipRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(protoBytes(1, testAgentRunRequest(t))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(connectFlagCompressed, buf.Bytes())
	out, _, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("rewrite not applied")
	}
	if out[0]&connectFlagCompressed == 0 {
		t.Fatal("gzip flag dropped")
	}
	assertConnectCursorRewrite(t, frame, true)
}

func TestRewriteConnectCursorUnchangedKeepsOriginalFrame(t *testing.T) {
	frame := connectFrame(0, protoString(5, "conv-only"))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if applied || stats.Changed {
		t.Fatalf("want unchanged skip, got applied=%v stats=%+v", applied, stats)
	}
	if !bytes.Equal(out, frame) {
		t.Fatal("unchanged rewrite re-encoded the frame")
	}
}

func TestRewriteConnectCursorConversationActionField4(t *testing.T) {
	uma := protoBytes(1, protoBytes(1, protoString(1, "locate the failing auth test")))
	action := protoBytes(1, uma)
	frame := connectFrame(0, protoBytes(4, action))
	out, _, catalog, _ := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if out == nil {
		t.Fatal("nil rewritten frame")
	}
	if catalog == nil {
		t.Fatal("catalog shape nil")
	}
	hasKey := false
	for _, k := range catalog.Keys {
		if k == "action" || strings.HasPrefix(k, "p") {
			hasKey = true
			break
		}
	}
	if !hasKey {
		t.Fatalf("catalog keys want action or proto fields, got %v", catalog.Keys)
	}
}

func TestRewriteConnectCursorExecClientMessageField2(t *testing.T) {
	frame := connectFrame(0, protoBytes(2, testAgentRunRequest(t)))
	assertConnectCursorRewrite(t, frame, true)
}

func TestRewriteConnectCursorExecClientControlField5(t *testing.T) {
	frame := connectFrame(0, protoBytes(5, testAgentRunRequest(t)))
	assertConnectCursorRewrite(t, frame, true)
}

func TestRewriteConnectCursorHeartbeatField7Unchanged(t *testing.T) {
	frame := connectFrame(0, protoString(7, "tick"))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if applied {
		t.Fatalf("heartbeat applied: stats=%+v", stats)
	}
	if !bytes.Equal(out, frame) {
		t.Fatal("heartbeat frame was rewritten")
	}
}

func TestRewriteConnectCursorExecResultTruncates(t *testing.T) {
	long := strings.Repeat("A", 2000)
	result := append(protoString(1, long), protoString(2, "SECRET_keep")...)
	exec := append(protoString(1, "exec-id-1"), protoBytes(7, result)...)
	frame := connectFrame(0, protoBytes(2, exec))
	out, stats, catalog, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied || !stats.Changed || !stats.CompactApplied {
		t.Fatalf("want exec result compact, applied=%v stats=%+v", applied, stats)
	}
	if len(out) >= len(frame) {
		t.Fatalf("rewritten frame not smaller: before=%d after=%d", len(frame), len(out))
	}
	keys := catalogKeys(catalog)
	hasP2 := false
	for _, k := range keys {
		if k == "p2" || strings.HasPrefix(k, "p2") {
			hasP2 = true
			break
		}
	}
	if !hasP2 {
		t.Fatalf("catalog missing p2: %v", keys)
	}
	payload := decodeConnectPayload(t, out)
	if !bytes.Contains(payload, []byte("jev-compaction truncated")) {
		t.Fatal("missing truncation stub")
	}
	if bytes.Contains(payload, []byte(long)) {
		t.Fatal("long result body still on the wire")
	}
	if !bytes.Contains(payload, []byte("SECRET_keep")) {
		t.Fatal("SECRET_ field was not preserved")
	}
	encoded, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SECRET_") {
		t.Fatalf("stats leaked SECRET_: %s", encoded)
	}
}

func TestRewriteConnectCursorKeepsHeartbeatWithAgentRun(t *testing.T) {
	hb := []byte("tick")
	raw := append(protoBytes(1, testAgentRunRequest(t)), protoBytes(7, hb)...)
	frame := connectFrame(0, raw)
	out, _, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("wrapped AgentRun was not applied")
	}
	payload := decodeConnectPayload(t, out)
	if !bytes.Equal(firstLD(payload, 7), hb) {
		t.Fatal("heartbeat field 7 was not byte-identical")
	}
}

func TestHandlerConnectProtoAgentRunRewritesFirstFrame(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoBytes(1, testAgentRunRequest(t)))
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	payload := decodeConnectPayload(t, got)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("upstream body is not AgentRunRequest")
	}
	if n := countMcpTools(run); n == 0 || n >= 4 {
		t.Fatalf("upstream tools=%d", n)
	}
	snap := srv.RunStats()
	sel, _ := snap["selectionApplied"].(int)
	comp, _ := snap["compactionApplied"].(int)
	if sel == 0 && comp == 0 {
		t.Fatalf("want selection or compaction applied: %+v", snap)
	}
	events, _ := snap["events"].([]Event)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ev := events[0]
	if ev.Apply == "" && ev.Reason == reasonStream {
		t.Fatalf("event not updated: %+v", ev)
	}
	if ev.Catalog == nil {
		t.Fatal("event catalog unset")
	}
}

func TestHandlerConnectProtoAgentRunRewritesLaterFrame(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame1 := connectFrame(0, protoBytes(1, testAgentRunRequestIDsOnly(t)))
	frame2 := connectFrame(0, protoBytes(1, testAgentRunRequest(t)))
	body := append(append([]byte{}, frame1...), frame2...)
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	r := bytes.NewReader(got)
	up1, ok1 := readConnectFrame(r)
	up2, ok2 := readConnectFrame(r)
	if !ok1 || !ok2 {
		t.Fatalf("want two complete frames ok1=%v ok2=%v got=%d", ok1, ok2, len(got))
	}
	rest, _ := io.ReadAll(r)
	if len(rest) != 0 {
		t.Fatalf("trailing bytes=%d", len(rest))
	}

	payload1 := decodeConnectPayload(t, up1)
	run1, _, ok := unwrapAgentRun(payload1)
	if !ok {
		t.Fatal("frame1 is not AgentRunRequest")
	}
	if protoFieldString(run1, 5) != "conv-agent-1" || protoFieldString(run1, 13) != "cursor" || protoFieldString(run1, 25) != "run-id-keep" || protoFieldString(run1, 26) != "sess-keep" {
		t.Fatalf("frame1 extra fields dropped id=%s harness=%s run=%s session=%s", protoFieldString(run1, 5), protoFieldString(run1, 13), protoFieldString(run1, 25), protoFieldString(run1, 26))
	}
	if n := countMcpTools(run1); n != 0 {
		t.Fatalf("frame1 tools=%d", n)
	}

	payload2 := decodeConnectPayload(t, up2)
	run2, _, ok := unwrapAgentRun(payload2)
	if !ok {
		t.Fatal("frame2 is not AgentRunRequest")
	}
	if n := countMcpTools(run2); n == 0 || n >= 4 {
		t.Fatalf("frame2 tools=%d", n)
	}

	snap := srv.RunStats()
	sel, _ := snap["selectionApplied"].(int)
	comp, _ := snap["compactionApplied"].(int)
	if sel < 1 || comp < 1 {
		t.Fatalf("want later-frame selection and compaction: %+v", snap)
	}
	events, _ := snap["events"].([]Event)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ev := events[0]
	if ev.ToolAfter >= ev.ToolBefore || !ev.CompactApplied {
		t.Fatalf("event not updated from later frame: %+v", ev)
	}
	encoded, _ := json.Marshal(snap)
	for _, leak := range []string{"FIND_THIS_PROMPT", "SECRET_ARG", "SECRET_RESULT"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("stats leaked %s: %s", leak, encoded)
		}
	}
	if ev.ConnectFrames < 2 {
		t.Fatalf("ConnectFrames=%d want >=2", ev.ConnectFrames)
	}
	if !catalogHasToolsOrP4(ev.Catalog) {
		t.Fatalf("catalog.keys=%v want tools/mcpTools or p4", catalogKeys(ev.Catalog))
	}
}

func TestHandlerConnectProtoCatalogUnionsEarlierFrameKeys(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame1 := connectFrame(0, protoBytes(1, testAgentRunRequest(t)))
	frame2 := connectFrame(0, protoBytes(1, protoString(5, "conv-agent-1")))
	body := append(append([]byte{}, frame1...), frame2...)
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	snap := srv.RunStats()
	events, _ := snap["events"].([]Event)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ev := events[0]
	if ev.ConnectFrames < 2 {
		t.Fatalf("ConnectFrames=%d want >=2", ev.ConnectFrames)
	}
	if !catalogHasToolsOrP4(ev.Catalog) {
		t.Fatalf("catalog.keys=%v want tools/mcpTools or p4 after conversation-id frame", catalogKeys(ev.Catalog))
	}
}

func TestHandlerConnectProtoAgentRunStreamIsNotBuffered(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", pr)
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("handler buffered the streaming body")
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestLiftAgentRunJSONArrayField1YieldsHistory(t *testing.T) {
	var objs []any
	for _, s := range cursorAgentHistJSON(t) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, obj)
	}
	arr, err := json.Marshal(objs)
	if err != nil {
		t.Fatal(err)
	}
	cs := protoString(1, string(arr))
	mcp := protoRepeated(1, [][]byte{
		testMcpTool("Read", "Read a file"),
		testMcpTool("Grep", "Search file contents"),
		testMcpTool("Shell", "Run a command"),
		testMcpTool("Write", "Write a file"),
	})
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, mcp)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)

	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	lifted, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	shape := catalogShape(lifted)
	if shape == nil || shape.HistoryTypes["conversationState.rootPromptMessagesJson.object"] < 1 {
		t.Fatalf("history=%v", shape.HistoryTypes)
	}
	assertConnectCursorRewrite(t, connectFrame(0, protoBytes(1, req)), true)
}

func TestLiftConversationTurnsUserMessageText(t *testing.T) {
	um := protoString(1, "hi from turn")
	act := protoBytes(1, um)
	turn := protoBytes(1, act)
	cs := protoBytes(8, turn)
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoString(5, "conv-turn-1")...)
	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	state, _ := root["conversationState"].(map[string]any)
	msgs := asSlice(state["rootPromptMessagesJson"])
	if len(msgs) != 1 {
		t.Fatalf("msgs=%v", msgs)
	}
	s, _ := msgs[0].(string)
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) != nil || obj["role"] != "user" {
		t.Fatalf("synthesized=%v", msgs[0])
	}
	frame := connectFrame(0, protoBytes(1, req))
	out, _, _, _ := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	payload := decodeConnectPayload(t, out)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("not AgentRunRequest")
	}
	got := firstLD(firstLD(run, 1), 8)
	if !bytes.Equal(got, turn) {
		t.Fatal("turn writeback is not a no-op")
	}
}

func TestLiftProtoUserMessageField1History(t *testing.T) {
	um := protoString(1, "FIND_THIS_PROMPT locate the failing auth test")
	cs := protoBytes(1, um)
	mcp := protoRepeated(1, [][]byte{
		testMcpTool("Read", "Read a file"),
		testMcpTool("Grep", "Search file contents"),
		testMcpTool("Shell", "Run a command"),
		testMcpTool("Write", "Write a file"),
	})
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, mcp)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)

	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	state, _ := root["conversationState"].(map[string]any)
	msgs := asSlice(state["rootPromptMessagesJson"])
	if len(msgs) != 1 {
		t.Fatalf("msgs=%v", msgs)
	}
	s, _ := msgs[0].(string)
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) != nil || obj["role"] != "user" {
		t.Fatalf("synthesized=%v", msgs[0])
	}
	lifted, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	shape := catalogShape(lifted)
	if shape == nil || shape.HistoryTypes["conversationState.rootPromptMessagesJson.object"] < 1 {
		t.Fatalf("history=%v", shape.HistoryTypes)
	}
	if len(shape.HistoryTypes) == 1 && shape.HistoryTypes["opaque_binary"] > 0 {
		t.Fatalf("historyTypes only opaque_binary: %v", shape.HistoryTypes)
	}

	frame := connectFrame(0, protoBytes(1, req))
	out, stats, catalog, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("rewrite not applied")
	}
	if catalog != nil && catalog.HistoryTypes["conversationState.rootPromptMessagesJson.object"] < 1 {
		t.Fatalf("catalog history=%v", catalog.HistoryTypes)
	}
	encoded, _ := json.Marshal(stats)
	if strings.Contains(string(encoded), "FIND_THIS_PROMPT") {
		t.Fatalf("stats leaked FIND_THIS_PROMPT: %s", encoded)
	}
	if catalog != nil {
		enc, _ := json.Marshal(catalog)
		if bytes.Contains(enc, []byte("FIND_THIS_PROMPT")) {
			t.Fatalf("catalog leaked FIND_THIS_PROMPT: %s", enc)
		}
	}
	payload := decodeConnectPayload(t, out)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("not AgentRunRequest")
	}
	got := firstLD(firstLD(run, 1), 1)
	if !bytes.Equal(got, um) {
		t.Fatal("proto field-1 writeback is not a no-op")
	}
}

func testCursorUserTurn(content any) []byte {
	b, err := json.Marshal(map[string]any{"role": "user", "content": content})
	if err != nil {
		panic(err)
	}
	return protoBytes(1, protoString(1, string(b)))
}

func testCursorAssistantTurn(content any) []byte {
	b, err := json.Marshal(map[string]any{"role": "assistant", "content": content})
	if err != nil {
		panic(err)
	}
	return protoBytes(2, protoString(1, string(b)))
}

func testAgentRunRequestProtoTurns(t *testing.T) []byte {
	t.Helper()
	turns := [][]byte{
		testCursorUserTurn("FIND_THIS_PROMPT locate the failing auth test"),
		testCursorAssistantTurn([]any{map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{"path": "SECRET_ARG"}}}),
		testCursorUserTurn([]any{map[string]any{"type": "tool_result", "tool_use_id": "a", "content": strings.Repeat("SECRET_RESULT\n", 300)}}),
		testCursorAssistantTurn([]any{map[string]any{"type": "tool_use", "id": "b", "name": "Read", "input": map[string]any{"path": "SECRET_ARG"}}}),
		testCursorUserTurn([]any{map[string]any{"type": "tool_result", "tool_use_id": "b", "content": strings.Repeat("SECRET_RESULT\n", 300)}}),
		testCursorAssistantTurn([]any{map[string]any{"type": "tool_use", "id": "c", "name": "Edit", "input": map[string]any{"path": "SECRET_ARG"}}}),
		testCursorUserTurn([]any{map[string]any{"type": "tool_result", "tool_use_id": "c", "content": strings.Repeat("SECRET_RESULT\n", 300)}}),
		testCursorUserTurn("The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."),
	}
	var cs []byte
	cs = append(cs, protoBytes(1, []byte{0x08, 0x01})...)
	cs = append(cs, protoString(4, "pending-keep")...)
	for _, turn := range turns {
		cs = append(cs, protoBytes(8, turn)...)
	}
	cs = append(cs, protoString(9, "keep-cs-sibling")...)
	mcp := protoRepeated(1, [][]byte{
		testMcpTool("Read", "Read a file"),
		testMcpTool("Grep", "Search file contents"),
		testMcpTool("Shell", "Run a command"),
		testMcpTool("Write", "Write a file"),
	})
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, mcp)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	for i := 0; i < 8; i++ {
		req = append(req, protoString(14, "composer")...)
	}
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)
	return req
}

func countProtoField(body []byte, field int) int {
	fields, ok := parseProtoFields(body)
	if !ok {
		return 0
	}
	n := 0
	for _, f := range fields {
		if f.field == field && f.wire == 2 {
			n++
		}
	}
	return n
}

func TestRewriteConnectCursorProtoTurnsAndToolName(t *testing.T) {
	req := testAgentRunRequestProtoTurns(t)
	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	msgs := asSlice(root["messages"])
	if len(msgs) != 8 {
		t.Fatalf("messages=%d root=%v", len(msgs), root)
	}
	user, assistant := 0, 0
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		switch m["role"] {
		case "user":
			user++
		case "assistant":
			assistant++
		}
	}
	if user == 0 || assistant == 0 {
		t.Fatalf("roles user=%d assistant=%d", user, assistant)
	}
	tools := cursorToolDefs(root)
	if len(tools) != 4 {
		t.Fatalf("tools=%d (p14 must not invent builtins)", len(tools))
	}
	frame := connectFrame(0, protoBytes(1, req))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("rewrite not applied")
	}
	if stats.ToolAfter >= stats.ToolBefore || stats.ToolBefore != 4 || !stats.CompactApplied {
		t.Fatalf("want filter+compact, got %+v", stats)
	}
	payload := decodeConnectPayload(t, out)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("not AgentRunRequest")
	}
	if protoFieldString(run, 5) != "conv-agent-1" || protoFieldString(run, 13) != "cursor" || protoFieldString(run, 26) != "sess-keep" {
		t.Fatalf("ids dropped id=%s harness=%s session=%s", protoFieldString(run, 5), protoFieldString(run, 13), protoFieldString(run, 26))
	}
	if protoFieldString(run, 25) != "run-id-keep" {
		t.Fatalf("unknown field 25 dropped: %q", protoFieldString(run, 25))
	}
	if countProtoField(run, 14) != 8 {
		t.Fatalf("p14 count=%d", countProtoField(run, 14))
	}
	cs := firstLD(run, 1)
	if protoFieldString(cs, 4) != "pending-keep" || protoFieldString(cs, 9) != "keep-cs-sibling" {
		t.Fatalf("conversation_state siblings dropped f4=%q f9=%q", protoFieldString(cs, 4), protoFieldString(cs, 9))
	}
	if countProtoField(cs, 8) != 8 {
		t.Fatalf("turns after writeback=%d", countProtoField(cs, 8))
	}
	after := countMcpTools(run)
	if after == 0 || after >= 4 {
		t.Fatalf("tools after=%d", after)
	}
	encoded, _ := json.Marshal(stats)
	for _, leak := range []string{"FIND_THIS_PROMPT", "SECRET_ARG", "SECRET_RESULT"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("stats leaked %s: %s", leak, encoded)
		}
	}
}

func TestRewriteConnectCursorField1JSONNoTurnsEmptyTools(t *testing.T) {
	hist := cursorAgentHistJSON(t)
	if len(hist) < 6 {
		t.Fatal("need 6 hist messages")
	}
	var cs []byte
	for _, s := range hist[:6] {
		cs = append(cs, protoString(1, s)...)
	}
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, cs)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, nil)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)

	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	msgs := asSlice(root["messages"])
	if len(msgs) != 6 {
		t.Fatalf("messages=%d root keys=%v", len(msgs), func() []string {
			keys := make([]string, 0, len(root))
			for k := range root {
				keys = append(keys, k)
			}
			return keys
		}())
	}
	frame := connectFrame(0, protoBytes(1, req))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied {
		t.Fatal("rewrite not applied")
	}
	if !stats.CompactApplied && stats.Protocol != "cursor" {
		t.Fatalf("want CompactApplied or protocol=cursor, got %+v", stats)
	}
	encoded, _ := json.Marshal(stats)
	for _, leak := range []string{"FIND_THIS_PROMPT", "SECRET_ARG", "SECRET_RESULT", "SECRET"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("stats leaked %s: %s", leak, encoded)
		}
	}
	payload := decodeConnectPayload(t, out)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("not AgentRunRequest")
	}
	gotCS := firstLD(run, 1)
	if countProtoField(gotCS, 1) != 6 {
		t.Fatalf("field 1 count=%d", countProtoField(gotCS, 1))
	}
	if countProtoField(gotCS, 8) != 0 {
		t.Fatalf("unexpected turns=%d", countProtoField(gotCS, 8))
	}
}

func TestRewriteConnectCursorGzipField1CatalogInner(t *testing.T) {
	hist := cursorAgentHistJSON(t)
	if len(hist) < 6 {
		t.Fatal("need 6 hist messages")
	}
	var cs []byte
	for _, s := range hist[:6] {
		cs = append(cs, protoString(1, s)...)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(cs); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gzipped := gz.Bytes()
	action := protoBytes(1, protoBytes(1, protoString(1, "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests.")))
	var req []byte
	req = append(req, protoBytes(1, gzipped)...)
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, nil)...)
	req = append(req, protoString(5, "conv-agent-1")...)
	req = append(req, protoString(13, "cursor")...)
	req = append(req, protoString(25, "run-id-keep")...)
	req = append(req, protoString(26, "sess-keep")...)

	cat := protoFieldCatalog(req)
	if cat == nil {
		t.Fatal("catalog is nil")
	}
	if cat.HistoryTypes["p1_gzip"] != 1 || cat.HistoryTypes["p1_ok"] != 1 {
		t.Fatalf("history=%v", cat.HistoryTypes)
	}
	if cat.HistoryTypes["p1_bytes"] != len(gzipped) {
		t.Fatalf("p1_bytes=%d want %d", cat.HistoryTypes["p1_bytes"], len(gzipped))
	}
	hasP1Inner := false
	for _, k := range cat.Keys {
		if strings.HasPrefix(k, "p1i_") {
			hasP1Inner = true
		}
		if strings.HasPrefix(k, "p4i_") {
			t.Fatalf("empty field 4 has inner key %s keys=%v", k, cat.Keys)
		}
	}
	if !hasP1Inner {
		t.Fatalf("missing p1 inner keys: %v", cat.Keys)
	}
	if v := cat.HistoryTypes["p4_bytes"]; v != 0 {
		t.Fatalf("p4_bytes=%d", v)
	}

	root, _, ok := liftAgentRunJSON(req)
	if !ok {
		t.Fatal("lift failed")
	}
	msgs := asSlice(root["messages"])
	if len(msgs) != 6 {
		t.Fatalf("messages=%d root keys=%v", len(msgs), func() []string {
			keys := make([]string, 0, len(root))
			for k := range root {
				keys = append(keys, k)
			}
			return keys
		}())
	}
	frame := connectFrame(0, protoBytes(1, req))
	_, stats, shape, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied && !stats.CompactApplied && stats.Protocol != "cursor" && stats.Apply != applyFilter {
		t.Fatalf("rewrite not applied and no cursor extract: %+v", stats)
	}
	if !stats.CompactApplied && stats.Protocol != "cursor" {
		t.Fatalf("RewriteWith did not extract messages: %+v", stats)
	}
	encoded, _ := json.Marshal(stats)
	catEnc, _ := json.Marshal(shape)
	for _, blob := range [][]byte{encoded, catEnc} {
		for _, leak := range []string{"FIND_THIS_PROMPT", "SECRET_ARG", "SECRET_RESULT", "SECRET"} {
			if bytes.Contains(blob, []byte(leak)) {
				t.Fatalf("event leaked %s: %s", leak, blob)
			}
		}
	}
}

func testRequestContextTools(prompt string, names ...string) []byte {
	var rc []byte
	if prompt != "" {
		rc = append(rc, protoString(21, prompt)...)
	}
	for _, name := range names {
		rc = append(rc, protoBytes(7, testMcpTool(name, name+" a file or search"))...)
	}
	return rc
}

func countRequestContextTools(rc []byte) int {
	fields, ok := parseProtoFields(rc)
	if !ok {
		return 0
	}
	n := 0
	for _, f := range fields {
		if f.field == 7 && f.wire == 2 {
			n++
		}
	}
	return n
}

func TestRewriteConnectCursorRequestContextToolsOnRun(t *testing.T) {
	prompt := "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."
	rc := testRequestContextTools(prompt, "Read", "Grep", "Shell", "Write")
	uma := append(protoBytes(1, protoString(1, prompt)), protoBytes(2, rc)...)
	req := protoBytes(2, protoBytes(1, uma))
	req = append(req, protoString(5, "conv-rc-1")...)
	frame := connectFrame(0, protoBytes(1, req))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied || stats.Apply != applyFilter || stats.ToolAfter >= stats.ToolBefore {
		t.Fatalf("want request_context filter, applied=%v stats=%+v", applied, stats)
	}
	payload := decodeConnectPayload(t, out)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		t.Fatal("not AgentRun")
	}
	action := firstLD(run, 2)
	gotRC := firstLD(firstLD(action, 1), 2)
	after := countRequestContextTools(gotRC)
	if after == 0 || after >= 4 {
		t.Fatalf("request_context tools after=%d", after)
	}
	if firstLD(run, 4) != nil {
		t.Fatal("invented mcpTools field 4")
	}
}

func TestRewriteConnectCursorExecRequestContextDedupesNames(t *testing.T) {
	prompt := "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."
	rc := testRequestContextTools(prompt, "Read", "Grep", "Read", "Grep", "Shell", "Write")
	frame := connectFrame(0, protoBytes(2, protoBytes(10, protoBytes(1, protoBytes(1, rc)))))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied || stats.Apply != applyFilter || stats.Reason == reasonDuplicateNames {
		t.Fatalf("duplicate names should be folded then filtered: applied=%v stats=%+v", applied, stats)
	}
	if stats.ToolAfter >= stats.ToolBefore {
		t.Fatalf("want shrink after dedupe, stats=%+v", stats)
	}
	payload := decodeConnectPayload(t, out)
	gotRC := firstLD(firstLD(firstLD(firstLD(payload, 2), 10), 1), 1)
	if n := countRequestContextTools(gotRC); n == 0 || n >= 6 {
		t.Fatalf("deduped tools after=%d", n)
	}
}

func TestRewriteConnectCursorExecRequestContextResult(t *testing.T) {
	prompt := "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."
	rc := testRequestContextTools(prompt, "Read", "Grep", "Shell", "Write")
	success := protoBytes(1, rc)
	rcr := protoBytes(1, success)
	exec := protoBytes(10, rcr)
	frame := connectFrame(0, protoBytes(2, exec))
	out, stats, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, localOpt())
	if !applied || stats.Apply != applyFilter || stats.ToolAfter >= stats.ToolBefore {
		t.Fatalf("want exec request_context filter, applied=%v stats=%+v", applied, stats)
	}
	payload := decodeConnectPayload(t, out)
	gotExec := firstLD(payload, 2)
	gotRC := firstLD(firstLD(firstLD(gotExec, 10), 1), 1)
	after := countRequestContextTools(gotRC)
	if after == 0 || after >= 4 {
		t.Fatalf("exec request_context tools after=%d", after)
	}
}

func TestHandlerConnectMergesExecCompactAndSelection(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	prompt := "The auth middleware test is failing. Find it, fix the assertion, and re-run the tests."
	run := connectFrame(0, protoBytes(1, protoBytes(2, protoBytes(1, protoBytes(1, protoString(1, prompt))))))
	rc := testRequestContextTools(prompt, "Read", "Grep", "Shell", "Write")
	sel := connectFrame(0, protoBytes(2, protoBytes(10, protoBytes(1, protoBytes(1, rc)))))
	long := strings.Repeat("B", 2000)
	result := protoString(1, long)
	comp := connectFrame(0, protoBytes(2, append(protoString(1, "exec-2"), protoBytes(7, result)...)))
	body := append(append(run, sel...), comp...)
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	snap := srv.RunStats()
	selN, _ := snap["selectionApplied"].(int)
	compN, _ := snap["compactionApplied"].(int)
	if selN == 0 || compN == 0 {
		t.Fatalf("want both gates: sel=%d compact=%d snap=%+v", selN, compN, snap)
	}
	events, _ := snap["events"].([]Event)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ev := events[0]
	if ev.Apply != applyFilter || !ev.CompactApplied {
		t.Fatalf("event merge lost flags: %+v", ev)
	}
	if !bytes.Contains(got, []byte("jev-compaction truncated")) {
		t.Fatal("upstream missing exec truncation")
	}
}

func skillReviewAgentRunRequest() []byte {
	action := protoBytes(1, protoBytes(1, protoString(1, "use the skill-review skill")))
	mcp := protoRepeated(1, [][]byte{testMcpTool("skill-review", "review skill")})
	var req []byte
	req = append(req, protoBytes(2, action)...)
	req = append(req, protoBytes(4, mcp)...)
	req = append(req, protoString(5, "conv-skill")...)
	req = append(req, protoString(13, "cursor")...)
	return req
}

func TestHandlerConnectCursorAutoAppliesSkill(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)
	opt := localOpt()
	opt.AutoApply = true
	opt.ApplicationPolicy = PolicyRequired
	opt.KindModes = map[string]string{"skill": KindApply}
	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoBytes(1, skillReviewAgentRunRequest()))
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if srv.LastDelivered == "" {
		t.Fatalf("skill not delivered on Connect run path: applyErr=%q", srv.ApplyErr)
	}
	payload := decodeConnectPayload(t, got)
	run, _, ok := unwrapAgentRun(payload)
	if !ok {
		run = payload
	}
	text := liftActionText(run)
	if !strings.Contains(text, "jev-routing context") && !strings.Contains(text, "review skill") {
		t.Fatalf("connect writeback missing skill context: %q body=%q", text, got)
	}
}

func TestHandlerConnectCursorObservesExecResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	srv.Apps.put(&Application{DecisionID: "dec-1", CallID: "call-1", State: AppStarted, Kind: "cli"})
	result := protoString(1, `{"ok":true}`)
	frame := connectFrame(0, protoBytes(2, append(protoString(1, "exec-2"), protoBytes(7, result)...)))
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := srv.Apps.Get("dec-1")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "ok") {
		t.Fatalf("exec result was not correlated: %+v", got)
	}
}

func TestHandlerConnectCursorObservesExecResultOutsideField7(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Cursor, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoBytes(2, append(protoString(1, "exec-8"), protoString(8, "jev-live-cli-ok\n")...)))
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := srv.Apps.Get("exec-8")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "jev-live-cli-ok") {
		t.Fatalf("field-8 exec result was not correlated: %+v apps=%v", got, srv.Apps.Snapshot())
	}
}

func TestConnectCursorWritesExistingUserContext(t *testing.T) {
	extra := "source: skill://review/SKILL.md"
	opt := localOpt()
	opt.AfterRewrite = func(body []byte) []byte {
		out, err := ApplyHostContext(host.Cursor, body, extra)
		if err != nil {
			t.Fatalf("writeback %v body=%s", err, body)
		}
		return out
	}
	frame := connectFrame(0, testAgentRunRequest(t))
	out, _, _, applied := rewriteConnectCursorFrame(t.Context(), frame, host.Cursor, nil, opt)
	if !applied {
		t.Fatal("expected context writeback on existing action text")
	}
	payload := decodeConnectPayload(t, out)
	req, _, ok := unwrapAgentRun(payload)
	if !ok {
		req = payload
	}
	text := liftActionText(req)
	if !strings.Contains(text, "auth middleware") {
		t.Fatalf("lost original user text: %q", text)
	}
	if !strings.Contains(text, extra) {
		t.Fatalf("missing delivered context: %q", text)
	}
}
