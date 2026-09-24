package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func devinPromptToolsJSON(t *testing.T) []byte {
	t.Helper()
	req := map[string]any{
		"prompt": "The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests." + strings.Repeat(" x", 1200),
		"tools": []any{
			map[string]any{"name": "read", "description": "Read a file"},
			map[string]any{"name": "grep", "description": "Search files"},
			map[string]any{"name": "edit", "description": "Edit a file"},
			map[string]any{"name": "exec", "description": "Run a command"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
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

func TestRewriteConnectDevinGetChatMessageFiltersPromptTools(t *testing.T) {
	raw := devinPromptToolsJSON(t)
	if len(raw) <= 2048 {
		t.Fatalf("fixture must exceed protoLikelyString cap, got %d", len(raw))
	}
	frame := connectFrame(0, protoString(1, string(raw)))
	out, stats, catalog, processed := rewriteConnectDevinFrame(t.Context(), frame, host.Devin, nil, localOpt())
	if !processed {
		t.Fatal("frame not processed")
	}
	if catalog == nil || catalog.Candidates == 0 {
		t.Fatalf("catalog=%+v", catalog)
	}
	if stats.Reason == reasonNotChat {
		t.Fatalf("devin prompt+tools was not_chat: %+v", stats)
	}
	if !stats.Changed || stats.ToolAfter != 1 || stats.Chosen != "grep" {
		t.Fatalf("want grep filter, got %+v", stats)
	}
	payload := decodeConnectPayload(t, out)
	got := firstLD(payload, 1)
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	if len(asSlice(obj["tools"])) != 1 {
		t.Fatalf("tools after=%v", obj["tools"])
	}
}

func TestHandlerConnectDevinGetChatMessageRewritesFirstFrame(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoString(1, string(devinPromptToolsJSON(t))))
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", strings.NewReader(string(frame)))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	payload := decodeConnectPayload(t, got)
	inner := firstLD(payload, 1)
	var obj map[string]any
	if err := json.Unmarshal(inner, &obj); err != nil {
		t.Fatal(err)
	}
	if len(asSlice(obj["tools"])) != 1 {
		t.Fatalf("upstream tools=%v", obj["tools"])
	}
	snap := srv.RunStats()
	events, _ := snap["events"].([]Event)
	if len(events) != 1 || events[0].Catalog == nil || events[0].Catalog.Candidates == 0 {
		t.Fatalf("event catalog unset: %+v", events)
	}
}

func TestRewriteConnectDevinProtoHistogramWhenNoJSON(t *testing.T) {
	var raw []byte
	raw = append(raw, protoString(1, "hello")...)
	raw = append(raw, protoString(3, "a")...)
	raw = append(raw, protoString(3, "b")...)
	raw = append(raw, protoString(7, "c")...)
	raw = append(raw, protoString(4, `{`)...)
	frame := connectFrame(0, raw)
	_, _, catalog, processed := rewriteConnectDevinFrame(t.Context(), frame, host.Devin, nil, localOpt())
	if !processed {
		t.Fatal("frame not processed")
	}
	if catalog == nil {
		t.Fatal("catalog is nil")
	}
	hasP1, hasP3 := false, false
	for _, k := range catalog.Keys {
		if k == "p1" {
			hasP1 = true
		}
		if k == "p3" || k == "p3x2" {
			hasP3 = true
		}
	}
	if !hasP1 || !hasP3 {
		t.Fatalf("keys=%v", catalog.Keys)
	}
	if catalog.HistoryTypes["proto_json"] < 1 {
		t.Fatalf("proto_json=%v", catalog.HistoryTypes)
	}
}

func TestHandlerConnectDevinGetChatMessageProtoCatalog(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	raw = append(raw, protoString(1, "hello")...)
	raw = append(raw, protoString(3, "a")...)
	raw = append(raw, protoString(3, "b")...)
	raw = append(raw, protoString(7, "c")...)
	frame := connectFrame(0, raw)
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", strings.NewReader(string(frame)))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if len(got) == 0 {
		t.Fatal("upstream got empty body")
	}
	snap := srv.RunStats()
	events, _ := snap["events"].([]Event)
	if len(events) != 1 || events[0].Catalog == nil {
		t.Fatalf("event catalog unset: %+v", events)
	}
	hasP1, hasP3 := false, false
	for _, k := range events[0].Catalog.Keys {
		if k == "p1" {
			hasP1 = true
		}
		if k == "p3" || k == "p3x2" {
			hasP3 = true
		}
	}
	if !hasP1 || !hasP3 {
		t.Fatalf("event keys=%v", events[0].Catalog.Keys)
	}
}

func devinNativeProtoTool(name, desc string) []byte {
	var b []byte
	b = append(b, protoString(1, name)...)
	b = append(b, protoString(2, desc)...)
	return b
}

func devinNativeGetChatMessage() []byte {
	prompt := "FIND_THIS_PROMPT The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."
	var raw []byte
	raw = append(raw, protoString(1, prompt)...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("Read", "Read a file"),
		devinNativeProtoTool("Grep", "Search file contents"),
		devinNativeProtoTool("Shell", "Run a command"),
		devinNativeProtoTool("Write", "Write a file"),
	})...)
	raw = append(raw, protoString(21, "SECRET")...)
	return raw
}

func TestRewriteConnectDevinNativeProtoCatalogFilters(t *testing.T) {
	raw := devinNativeGetChatMessage()
	frame := connectFrame(0, raw)
	out, stats, catalog, processed := rewriteConnectDevinFrame(t.Context(), frame, host.Devin, nil, localOpt())
	if !processed {
		t.Fatal("frame not processed")
	}
	if catalog == nil || catalog.Candidates == 0 {
		t.Fatalf("catalog=%+v", catalog)
	}
	if stats.Reason == reasonNotChat {
		t.Fatalf("native proto catalog was not_chat: %+v", stats)
	}
	if !stats.Changed || stats.ToolAfter >= stats.ToolBefore || stats.Chosen != "Grep" {
		t.Fatalf("want Grep filter ToolAfter < ToolBefore, got %+v", stats)
	}
	encoded, _ := json.Marshal(stats)
	if bytes.Contains(encoded, []byte("FIND_THIS_PROMPT")) || bytes.Contains(encoded, []byte("SECRET")) {
		t.Fatalf("stats leaked secret: %s", encoded)
	}
	if catalog != nil {
		enc, _ := json.Marshal(catalog)
		if bytes.Contains(enc, []byte("FIND_THIS_PROMPT")) || bytes.Contains(enc, []byte("SECRET")) {
			t.Fatalf("catalog leaked secret: %s", enc)
		}
	}
	payload := decodeConnectPayload(t, out)
	fields, ok := parseProtoFields(payload)
	if !ok {
		t.Fatal("rewritten payload is not proto")
	}
	var tools [][]byte
	f1, f21 := "", ""
	for _, f := range fields {
		switch {
		case f.field == 1 && f.wire == 2:
			f1 = string(f.raw)
		case f.field == 10 && f.wire == 2:
			tools = append(tools, f.raw)
		case f.field == 21 && f.wire == 2:
			f21 = string(f.raw)
		}
	}
	if f21 != "SECRET" {
		t.Fatalf("field 21 not preserved: %q", f21)
	}
	if !strings.Contains(f1, "auth middleware test is failing") {
		t.Fatalf("field 1 prompt rewritten: %q", f1)
	}
	if len(tools) != 1 {
		t.Fatalf("field 10 tools after=%d", len(tools))
	}
	name, _ := devinToolNameDesc(tools[0])
	if name != "Grep" {
		t.Fatalf("kept tool %q", name)
	}
	if !bytes.Equal(tools[0], devinNativeProtoTool("Grep", "Search file contents")) {
		t.Fatal("kept tool bytes were rewritten")
	}
}

func TestHandlerConnectDevinGetChatMessageNativeProtoRewrites(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, devinNativeGetChatMessage())
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", strings.NewReader(string(frame)))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	payload := decodeConnectPayload(t, got)
	n := 0
	fields, ok := parseProtoFields(payload)
	if !ok {
		t.Fatal("upstream body is not proto")
	}
	for _, f := range fields {
		if f.field == 10 && f.wire == 2 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("upstream field 10 tools=%d", n)
	}
	snap := srv.RunStats()
	events, _ := snap["events"].([]Event)
	rewritten, _ := snap["rewritten"].(int)
	applied, _ := snap["selectionApplied"].(int)
	if rewritten == 0 && applied == 0 && (len(events) == 0 || !events[0].Changed) {
		t.Fatalf("native proto not rewritten: snap=%v events=%+v", snap, events)
	}
}

func devinNativeChatItem(role, text, tool string) []byte {
	var b []byte
	if role != "" {
		b = append(b, protoString(1, role)...)
	}
	if text != "" {
		b = append(b, protoString(2, text)...)
	}
	if tool != "" {
		b = append(b, protoString(3, tool)...)
	}
	return b
}

func devinSecretResultBody() string {
	return strings.Repeat("A", 80) + "SECRET_RESULT" + strings.Repeat("Z", 400)
}

func devinNativeGetChatMessageWithHistory() []byte {
	secret := devinSecretResultBody()
	args := "path=/tmp/auth_middleware_test.go offset=0"
	user := "FIND_THIS_PROMPT The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."
	var raw []byte
	raw = append(raw, protoString(1, user)...)
	raw = append(raw, protoRepeated(3, [][]byte{
		devinNativeChatItem("assistant", args, "Read"),
		devinNativeChatItem("tool", secret, "Read"),
		devinNativeChatItem("assistant", args+"#2", "Read"),
		devinNativeChatItem("tool", secret, "Read"),
		devinNativeChatItem("assistant", "pattern=auth middleware", "Grep"),
		devinNativeChatItem("tool", secret, "Grep"),
		devinNativeChatItem("assistant", "path=/tmp/out.go", "Write"),
		devinNativeChatItem("user", user, ""),
	})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("Read", "Read a file"),
		devinNativeProtoTool("Grep", "Search file contents"),
		devinNativeProtoTool("Shell", "Run a command"),
		devinNativeProtoTool("Write", "Write a file"),
	})...)
	raw = append(raw, protoString(21, "KEEP_FIELD21")...)
	return raw
}

func TestObserveDevinNativeHistoryPairsEveryCallAndResult(t *testing.T) {
	s := &Server{Host: host.Devin, Apps: NewAppStore(), Executor: &recordingExec{}}
	s.observeHostFrames(connectFrame(0, devinNativeGetChatMessageWithHistory()))
	for _, id := range []string{"c1", "c2", "c3"} {
		app := s.Apps.Get(id)
		if app == nil || app.State != AppVerified || app.CallID != id {
			t.Fatalf("%s was not paired with its result: %+v", id, app)
		}
	}
	app := s.Apps.Get("c4")
	if app == nil || app.State != AppStarted {
		t.Fatalf("unfinished call must remain started: %+v", app)
	}
}

func TestObserveDevinNativeHistoryRejectsNonCatalogCallIDAsToolName(t *testing.T) {
	var raw []byte
	raw = append(raw, protoString(1, "Read go.mod")...)
	raw = append(raw, protoRepeated(3, [][]byte{
		devinNativeChatItem("assistant", "path=go.mod", "call_abc123"),
		devinNativeChatItem("tool", "module example.org/test", "call_abc123"),
	})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("Read", "Read a file"),
		devinNativeProtoTool("Write", "Write a file"),
		devinNativeProtoTool("Shell", "Run a command"),
	})...)
	s := &Server{Host: host.Devin, Apps: NewAppStore(), Executor: &recordingExec{}}
	s.observeHostFrames(connectFrame(0, raw))
	if got := s.Apps.Snapshot(); len(got) != 0 {
		t.Fatalf("non-catalog call ID was misclassified as a tool: %+v", *got[0])
	}
}

func TestObserveDevinNativeWireToolCallAndResult(t *testing.T) {
	const id = "call_abcdefghijklmnop"
	call := append(protoString(1, id), protoString(2, "read")...)
	call = append(call, protoString(3, `{"path":"go.mod"}`)...)
	assistant := append(protoString(1, "11111111-1111-1111-1111-111111111111"), protoBytes(6, call)...)
	result := append(protoString(1, "22222222-2222-2222-2222-222222222222"), protoString(3, "module example.org/test")...)
	result = append(result, protoString(7, id)...)
	var raw []byte
	raw = append(raw, protoString(1, "Read go.mod")...)
	raw = append(raw, protoRepeated(3, [][]byte{assistant, result})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("read", "Read a file"),
		devinNativeProtoTool("write", "Write a file"),
		devinNativeProtoTool("exec", "Run command"),
	})...)
	s := &Server{Host: host.Devin, Apps: NewAppStore(), Executor: &recordingExec{}}
	s.observeHostFrames(connectFrame(0, raw))
	app := s.Apps.Get(id)
	if app == nil || app.State != AppVerified || app.CallID != id || !strings.Contains(app.Result, "module example.org/test") {
		t.Fatalf("real native wire pair not observed: %+v", app)
	}
}

func TestLiftDevinNativeTextDoesNotTreatMessageIDAsTool(t *testing.T) {
	item := append(protoString(1, "a1111111-1111-1111-1111-111111111111"), protoString(3, "Read the module file")...)
	got, ok := liftDevinChatMessage(item)
	if !ok || len(asSlice(got["tool_calls"])) != 0 || got["_tool"] != nil || got["content"] != "Read the module file" {
		t.Fatalf("message ID became tool call: %+v", got)
	}
}

func TestObserveDevinProtoLeafRequiresCallID(t *testing.T) {
	s := &Server{Host: host.Devin, Apps: NewAppStore(), Executor: &recordingExec{}}
	s.observeHostFrames(connectFrame(0, protoString(1, "task")))
	if got := s.Apps.Snapshot(); len(got) != 0 {
		t.Fatalf("tool name without call ID was recorded: %+v", *got[0])
	}
}

func TestObserveDevinProtoLeafRejectsPaddedNames(t *testing.T) {
	s := &Server{Host: host.Devin, Apps: NewAppStore(), Executor: &recordingExec{}}
	for _, name := range []string{" task", " bash"} {
		raw := append(protoString(1, name), protoString(12, "f1becc7a-3c48-4004-b4c0-9e2ea2894417")...)
		s.observeHostFrames(connectFrame(0, raw))
	}
	// Catalog and history leaves must not be paired with a top-level message ID.
	s.observeHostFrames(connectFrame(0, append(protoString(2, "task"), protoString(12, "f1becc7a-3c48-4004-b4c0-9e2ea2894417")...)))
	if got := s.Apps.Snapshot(); len(got) != 0 {
		t.Fatalf("padded proto leaves became tool calls: %+v", got)
	}
}

func TestRewriteConnectDevinNativeProtoKeepsHistory(t *testing.T) {
	raw := devinNativeGetChatMessageWithHistory()
	frame := connectFrame(0, raw)
	out, stats, catalog, processed := rewriteConnectDevinFrame(t.Context(), frame, host.Devin, nil, localOpt())
	if !processed {
		t.Fatal("frame not processed")
	}
	if catalog == nil || catalog.Candidates == 0 {
		t.Fatalf("catalog=%+v", catalog)
	}
	hasP3Inner, hasP10Inner, hasP3Text := false, false, false
	for _, k := range catalog.Keys {
		if strings.HasPrefix(k, "p3i_p") {
			hasP3Inner = true
		}
		if strings.HasPrefix(k, "p10i_p") {
			hasP10Inner = true
		}
	}
	for k := range catalog.HistoryTypes {
		if strings.HasPrefix(k, "p3_text") {
			hasP3Text = true
		}
	}
	if !hasP3Inner || !hasP10Inner || !hasP3Text {
		t.Fatalf("catalog keys=%v history=%v", catalog.Keys, catalog.HistoryTypes)
	}
	if stats.Protocol == "prompt" {
		t.Fatalf("history lifted as prompt: %+v", stats)
	}
	// History is compacted only on the agent's own compaction request.
	if stats.CompactApplied {
		t.Fatalf("history compacted without native compaction: %+v", stats)
	}
	if !stats.Changed || stats.ToolAfter >= stats.ToolBefore {
		t.Fatalf("want tool filter, got %+v", stats)
	}
	encoded, _ := json.Marshal(stats)
	if bytes.Contains(encoded, []byte("SECRET_RESULT")) || bytes.Contains(encoded, []byte("FIND_THIS_PROMPT")) {
		t.Fatalf("stats leaked secret: %s", encoded)
	}
	if catalog != nil {
		enc, _ := json.Marshal(catalog)
		if bytes.Contains(enc, []byte("SECRET_RESULT")) || bytes.Contains(enc, []byte("FIND_THIS_PROMPT")) {
			t.Fatalf("catalog leaked secret: %s", enc)
		}
	}
	payload := decodeConnectPayload(t, out)
	fields, ok := parseProtoFields(payload)
	if !ok {
		t.Fatal("rewritten payload is not proto")
	}
	var hist [][]byte
	var tools [][]byte
	f21 := ""
	for _, f := range fields {
		switch {
		case f.field == 3 && f.wire == 2:
			hist = append(hist, f.raw)
		case f.field == 10 && f.wire == 2:
			tools = append(tools, f.raw)
		case f.field == 21 && f.wire == 2:
			f21 = string(f.raw)
		}
	}
	if f21 != "KEEP_FIELD21" {
		t.Fatalf("field 21 not preserved: %q", f21)
	}
	if len(tools) == 0 || len(tools) >= 4 {
		t.Fatalf("field 10 tools after=%d", len(tools))
	}
	origFields, ok := parseProtoFields(raw)
	if !ok {
		t.Fatal("parse original proto")
	}
	var origHist [][]byte
	for _, f := range origFields {
		if f.field == 3 && f.wire == 2 {
			origHist = append(origHist, f.raw)
		}
	}
	if len(hist) != len(origHist) {
		t.Fatalf("hist count changed: before=%d after=%d", len(origHist), len(hist))
	}
	for i := range hist {
		if !bytes.Equal(hist[i], origHist[i]) {
			t.Fatalf("hist item %d changed", i)
		}
	}
}

func TestLiftDevinHistoryKeepsPartialMessages(t *testing.T) {
	user := "FIND_THIS_PROMPT keep this turn"
	var raw []byte
	raw = append(raw, protoString(1, "fallback prompt only")...)
	raw = append(raw, protoRepeated(3, [][]byte{
		devinNativeChatItem("user", user+" one", ""),
		devinNativeChatItem("assistant", "noted the first request", ""),
		devinNativeChatItem("user", user+" two", ""),
		{},
	})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("Read", "Read a file"),
		devinNativeProtoTool("Grep", "Search file contents"),
		devinNativeProtoTool("Shell", "Run a command"),
		devinNativeProtoTool("Write", "Write a file"),
	})...)
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		t.Fatal("native proto did not lift")
	}
	msgs := asSlice(lift.root["messages"])
	if len(msgs) < 2 {
		t.Fatalf("partial history fell back to prompt-only: root=%v", lift.root)
	}
	if _, hasPrompt := lift.root["prompt"]; hasPrompt {
		t.Fatalf("partial history used prompt protocol: %v", lift.root)
	}
	if lift.histField != 3 {
		t.Fatalf("histField=%d", lift.histField)
	}
}

func TestWritebackDevinHistoryKeepsRemainingByIdx(t *testing.T) {
	raw := devinNativeGetChatMessageWithHistory()
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		t.Fatal("native proto did not lift")
	}
	msgs := asSlice(lift.root["messages"])
	if len(msgs) < 4 {
		t.Fatalf("msgs=%d", len(msgs))
	}
	round, err := json.Marshal(map[string]any{"messages": msgs[2:]})
	if err != nil {
		t.Fatal(err)
	}
	var next map[string]any
	if err := json.Unmarshal(round, &next); err != nil {
		t.Fatal(err)
	}
	remaining := asSlice(next["messages"])
	out, wrote := writebackDevinHistory(raw, lift, next)
	if !wrote {
		t.Fatal("expected history writeback for dropped call/result pair")
	}
	fields, ok := parseProtoFields(out)
	if !ok {
		t.Fatal("parseProtoFields failed")
	}
	origFields, ok := parseProtoFields(raw)
	if !ok {
		t.Fatal("parse original proto")
	}
	var origHist, hist [][]byte
	for _, f := range origFields {
		if f.field == lift.histField && f.wire == 2 {
			origHist = append(origHist, f.raw)
		}
	}
	for _, f := range fields {
		if f.field == lift.histField && f.wire == 2 {
			hist = append(hist, f.raw)
		}
	}
	if len(hist) != len(origHist) || len(hist) == 0 {
		t.Fatalf("hist=%d orig=%d remaining=%d", len(hist), len(origHist), len(remaining))
	}
	if !bytes.Contains(hist[0], []byte("jev-compaction truncated")) {
		t.Fatal("dropped hist item was deleted instead of stubbed")
	}
	if !bytes.Equal(hist[2], lift.histItems[2].raw) {
		t.Fatal("kept proto items were not matched by _idx")
	}
}

func TestWritebackDevinHistoryPreservesCompactDroppedSlots(t *testing.T) {
	unlifted := []byte{0x08, 0x07}
	user := "FIND_THIS_PROMPT keep this turn"
	var raw []byte
	raw = append(raw, protoString(1, "fallback prompt only")...)
	raw = append(raw, protoRepeated(3, [][]byte{
		devinNativeChatItem("user", user+" one", ""),
		devinNativeChatItem("assistant", "noted the first request", ""),
		unlifted,
		devinNativeChatItem("user", user+" two", ""),
	})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("Read", "Read a file"),
		devinNativeProtoTool("Grep", "Search file contents"),
		devinNativeProtoTool("Shell", "Run a command"),
		devinNativeProtoTool("Write", "Write a file"),
	})...)
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		t.Fatal("native proto did not lift")
	}
	msgs := asSlice(lift.root["messages"])
	if len(msgs) != 3 {
		t.Fatalf("lifted=%d", len(msgs))
	}
	round, err := json.Marshal(map[string]any{"messages": msgs[2:]})
	if err != nil {
		t.Fatal(err)
	}
	var next map[string]any
	if err := json.Unmarshal(round, &next); err != nil {
		t.Fatal(err)
	}
	out, wrote := writebackDevinHistory(raw, lift, next)
	if !wrote {
		t.Fatal("expected history writeback for compact-dropped turns")
	}
	fields, ok := parseProtoFields(out)
	if !ok {
		t.Fatal("parseProtoFields failed after writeback")
	}
	var hist [][]byte
	for _, f := range fields {
		if f.field == lift.histField && f.wire == 2 {
			hist = append(hist, f.raw)
		}
	}
	if len(hist) != 4 {
		t.Fatalf("hist count changed: %d", len(hist))
	}
	if !bytes.Contains(hist[0], []byte("jev-compaction truncated")) {
		t.Fatal("dropped hist item missing jev-compaction truncated")
	}
	if !bytes.Contains(hist[1], []byte("jev-compaction truncated")) {
		t.Fatal("dropped assistant item missing jev-compaction truncated")
	}
	if !bytes.Equal(hist[2], unlifted) {
		t.Fatal("unlifted sibling disappeared")
	}
	if !bytes.Equal(hist[3], lift.histItems[2].raw) {
		t.Fatal("kept user item was not matched by _idx")
	}
}

func TestWritebackDevinHistoryMatchesTruncatedUserByIdx(t *testing.T) {
	raw := devinNativeGetChatMessageWithHistory()
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		t.Fatal("native proto did not lift")
	}
	round, err := json.Marshal(lift.root["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var msgs []any
	if err := json.Unmarshal(round, &msgs); err != nil {
		t.Fatal(err)
	}
	truncated := false
	for _, rawMsg := range msgs {
		m, ok := rawMsg.(map[string]any)
		if !ok || m["role"] != "user" {
			continue
		}
		m["content"] = "jev-compaction truncated: find the failing test"
		truncated = true
	}
	if !truncated {
		t.Fatal("no user message to truncate")
	}
	out, wrote := writebackDevinHistory(raw, lift, map[string]any{"messages": msgs})
	if !wrote {
		t.Fatal("truncated user text did not match via _idx")
	}
	if !bytes.Contains(out, []byte("jev-compaction truncated: find the failing test")) {
		t.Fatal("truncated user text was not written back")
	}
}

func devinChatJSONWithModel(t *testing.T, prompt string) []byte {
	t.Helper()
	req := map[string]any{
		"model":  "devstral-test",
		"prompt": prompt + strings.Repeat(" x", 1200),
		"tools": []any{
			map[string]any{"name": "read", "description": "Read a file"},
			map[string]any{"name": "grep", "description": "Search files"},
			map[string]any{"name": "edit", "description": "Edit a file"},
			map[string]any{"name": "exec", "description": "Run a command"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHandlerConnectDevinRecordsJevAttempt(t *testing.T) {
	jevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "fake",
			"usage": map[string]any{"input_tokens": 31, "output_tokens": 7},
			"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9, "probabilities": adoptTestProbs("grep")},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			},
		})
	}))
	defer jevSrv.Close()
	client := &jev.Client{APIKey: "test", BaseURL: jevSrv.URL, Model: "fake", HTTP: jevSrv.Client()}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, client, io.Discard, steerOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoString(1, string(devinChatJSONWithModel(t, "zzz qwerty unmatched words no known task pattern"))))
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", strings.NewReader(string(frame)))
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
	e := events[0]
	if e.JevCalls != 1 || len(e.JevAttempts) != 1 {
		t.Fatalf("jevCalls=%d attempts=%d", e.JevCalls, len(e.JevAttempts))
	}
	if e.JevAttempts[0].Purpose != "selection" || !e.JevAttempts[0].OK {
		t.Fatalf("attempt=%+v", e.JevAttempts[0])
	}
	if got := e.JevAttempts[0].InputTokens; got == nil || *got != 31 {
		t.Fatalf("jev input tokens=%v", got)
	}
	if got := e.JevAttempts[0].OutputTokens; got == nil || *got != 7 {
		t.Fatalf("jev output tokens=%v", got)
	}
	if e.Confidence == nil || e.NeedsTool == nil {
		t.Fatalf("confidence=%v needsTool=%v", e.Confidence, e.NeedsTool)
	}
	if e.Source != "jev" || e.Chosen != "grep" {
		t.Fatalf("source=%q chosen=%q", e.Source, e.Chosen)
	}
	if e.OriginalModel != "devstral-test" || e.SentModel != "devstral-test" {
		t.Fatalf("models=%q/%q", e.OriginalModel, e.SentModel)
	}
	if n, _ := snap["jevHTTP"].(int); n != 1 {
		t.Fatalf("jevHTTP=%v", snap["jevHTTP"])
	}
	if n, _ := snap["jevOK"].(int); n != 1 {
		t.Fatalf("jevOK=%v", snap["jevOK"])
	}
}

func TestHandlerConnectDevinEndStreamErrorMarksFinish(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("content-type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		var body []byte
		body = append(body, connectFrame(0, protoString(1, `{"delta":"ok"}`))...)
		body = append(body, connectFrame(connectFlagEndStream, []byte(`{"error":{"code":"internal","message":"boom"}}`))...)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoString(1, string(devinPromptToolsJSON(t))))
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", strings.NewReader(string(frame)))
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
	if events[0].UpstreamFinish != "error" {
		t.Fatalf("upstreamFinish=%q", events[0].UpstreamFinish)
	}
}

func TestWritebackDevinHistoryAbortsUnmatchedRemaining(t *testing.T) {
	raw := devinNativeGetChatMessageWithHistory()
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		t.Fatal("native proto did not lift")
	}
	next := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "assistant",
				"content": "pattern=auth middleware",
				"tool_calls": []any{
					map[string]any{
						"id":       "",
						"type":     "function",
						"function": map[string]any{"name": "Grep", "arguments": "pattern=auth middleware"},
					},
				},
			},
		},
	}
	out, wrote := writebackDevinHistory(raw, lift, next)
	if wrote {
		t.Fatal("unmatched remaining rewrote history")
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("history bytes changed for unmatched remaining")
	}
}

func TestLiftDevinChatMessageLeavesOrchestratorRoleEmpty(t *testing.T) {
	brief := "You are Devin. Search the codebase thoroughly and delegate multi-step work."
	got, ok := liftDevinChatMessage(devinNativeChatItem("", brief, ""))
	if !ok {
		t.Fatal("lift failed")
	}
	if got["role"] != "" {
		t.Fatalf("orchestrator brief became role=%q", got["role"])
	}
	ask, ok := liftDevinChatMessage(devinNativeChatItem("", "定義を検索して本文を読んでください", ""))
	if !ok {
		t.Fatal("ask lift failed")
	}
	if ask["role"] != "user" {
		t.Fatalf("japanese ask role=%q", ask["role"])
	}
}

func TestHandlerConnectDevinGetChatMessageStreamIsNotBuffered(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", pr)
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

func TestProtoFieldCatalogSingletonWire2Inner(t *testing.T) {
	inner := protoRepeated(1, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
	body := protoBytes(4, inner)
	cat := protoFieldCatalog(body)
	if cat == nil {
		t.Fatal("catalog is nil")
	}
	has := false
	for _, k := range cat.Keys {
		if k == "p4i_p1" || k == "p4i_p1x3" {
			has = true
			break
		}
	}
	if !has {
		t.Fatalf("keys=%v", cat.Keys)
	}
}

func TestMergeDevinCatalogKeepsMaxP1Bytes(t *testing.T) {
	earlier := &CatalogShape{HistoryTypes: map[string]int{"p1_bytes": 4096, "p1_ok": 1}}
	later := &CatalogShape{HistoryTypes: map[string]int{"p1_bytes": 0, "p1_ok": 1}}
	got := mergeDevinCatalog(earlier, later)
	if got.HistoryTypes["p1_bytes"] != 4096 {
		t.Fatalf("p1_bytes=%d want 4096", got.HistoryTypes["p1_bytes"])
	}
}

func TestHandlerConnectDevinObservesNativeExecCallFrame(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	call := connectFrame(0, append(append(
		protoString(1, "exec"),
		protoString(2, `{"command": "echo jev-live-cli-ok"}`)...,
	), protoString(12, "ae6ad5f5-082e-4179-ba17-a8a51269e9ae")...))
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", bytes.NewReader(call))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	started := srv.Apps.Get("ae6ad5f5-082e-4179-ba17-a8a51269e9ae")
	if started == nil || started.State != AppStarted {
		t.Fatalf("native exec call was not started: %+v", srv.Apps.Snapshot())
	}
	result := connectFrame(0, protoString(1, "Output from command in shell 14e7f1:\njev-live-cli-ok\n\n\nExit code: 0"))
	req = httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", bytes.NewReader(result))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	got := srv.Apps.Get("ae6ad5f5-082e-4179-ba17-a8a51269e9ae")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "jev-live-cli-ok") {
		t.Fatalf("native exec result was not correlated: %+v", got)
	}
}

func TestHandlerConnectDevinObservesExecHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	hist := func(parts ...string) []byte {
		var b []byte
		for i, p := range parts {
			b = append(b, protoString(i+1, p)...)
		}
		return b
	}
	var raw []byte
	raw = append(raw, protoRepeated(3, [][]byte{
		hist("user", "run this exact exec tool: echo jev-live-cli-ok"),
		hist("assistant", "exec", `{"command":"echo jev-live-cli-ok"}`),
		hist("tool", "exec", "jev-live-cli-ok\n"),
	})...)
	raw = append(raw, protoRepeated(10, [][]byte{
		devinNativeProtoTool("read", "Read a file"),
		devinNativeProtoTool("edit", "Edit a file"),
		devinNativeProtoTool("exec", "Run a command"),
		devinNativeProtoTool("get_output", "Read command output"),
	})...)
	frame := connectFrame(0, raw)
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var got *Application
	for _, app := range srv.Apps.Snapshot() {
		if app.State == AppVerified && strings.Contains(app.Result, "jev-live-cli-ok") {
			got = app
			break
		}
	}
	if got == nil {
		t.Fatalf("devin exec history was not correlated: %+v", srv.Apps.Snapshot())
	}
}

func TestHandlerConnectDevinAutoAppliesSkill(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)
	opt := localOpt()
	opt.AutoApply = true
	opt.ApplicationPolicy = PolicyRequired
	opt.KindModes = map[string]string{"skill": KindApply}
	srv, err := NewWithOptions("127.0.0.1:0", host.Devin, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"prompt": "use the skill-review skill",
		"tools": []any{
			map[string]any{"name": "skill-review", "description": "review skill"},
			map[string]any{"name": "read", "description": "Read a file"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	frame := connectFrame(0, protoString(1, string(raw)))
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if srv.LastDelivered == "" {
		t.Fatalf("skill not delivered on Devin Connect run path: applyErr=%q", srv.ApplyErr)
	}
	payload := decodeConnectPayload(t, got)
	inner := firstLD(payload, 1)
	if !bytes.Contains(inner, []byte("jev-routing context")) {
		t.Fatalf("devin writeback missing skill context: %s", inner)
	}
}

func TestConnectDevinWritesExistingPromptContext(t *testing.T) {
	extra := "source: skill://review/SKILL.md"
	opt := localOpt()
	opt.AfterRewrite = func(_ context.Context, body []byte) []byte {
		out, err := ApplyHostContext(host.Devin, body, extra)
		if err != nil {
			t.Fatalf("writeback %v body=%s", err, body)
		}
		return out
	}
	raw := devinPromptToolsJSON(t)
	frame := connectFrame(0, protoString(1, string(raw)))
	out, _, _, processed := rewriteConnectDevinFrame(t.Context(), frame, host.Devin, nil, opt)
	if !processed {
		t.Fatal("frame not processed")
	}
	payload := decodeConnectPayload(t, out)
	got := firstLD(payload, 1)
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	prompt, _ := obj["prompt"].(string)
	if !strings.Contains(prompt, "auth middleware") {
		t.Fatalf("lost original prompt: %q", prompt)
	}
	if !strings.Contains(prompt, extra) {
		t.Fatalf("missing delivered context: %q", prompt)
	}
	if _, ok := obj["tool_choice"]; ok {
		t.Fatal("invented tool_choice")
	}
}
