package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

const connectFlagCompressed byte = 0x01
const connectFlagEndStream byte = 0x02

func protoString(field int, s string) []byte {
	return protoTagAndBytes(uint64(field)<<3|2, []byte(s))
}

func protoBytes(field int, raw []byte) []byte {
	return protoTagAndBytes(uint64(field)<<3|2, raw)
}

func protoRepeated(field int, items [][]byte) []byte {
	var out []byte
	for _, it := range items {
		out = append(out, protoBytes(field, it)...)
	}
	return out
}

func connectFrame(flags byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func connectCursorContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "proto")
}

func wrapConnectCursorBody(body io.ReadCloser, s *Server, seq int64, ctx context.Context) io.ReadCloser {
	return &lazyConnectCursor{src: body, s: s, seq: seq, ctx: ctx}
}

// lazyConnectCursor rewrites each Connect envelope as Read pulls it
// so Handler can call ServeHTTP without waiting for the bidi body.
// First Read consumes only the first 5+len bytes.
type lazyConnectCursor struct {
	src io.ReadCloser
	s   *Server
	seq int64
	ctx context.Context

	buf []byte
	err error
}

type cursorTurnItem struct {
	idx int
	raw []byte
	msg map[string]any
}

type cursorAgentLift struct {
	root        map[string]any
	toolBytes   map[string][]byte
	histItems   []cursorTurnItem
	field1Items []cursorTurnItem
}

func (l *lazyConnectCursor) Read(p []byte) (int, error) {
	ctx := l.ctx
	if task := l.s.cursorTaskCopy(); task != "" {
		ctx = context.WithValue(ctx, cursorTaskKey{}, task)
	}
	return emitConnectFrame(p, &l.buf, &l.err, l.src, func(frame []byte) []byte {
		out, stats, catalog, applied := rewriteConnectCursorFrame(ctx, frame, l.s.Host, l.s.Client, l.s.Options)
		observeConnectFrame(l.s.events, l.seq, catalog)
		l.s.observeHostFrames(frame)
		if task := cursorTaskFromFrame(frame); task != "" {
			l.s.setCursorTask(task)
		}
		if applied {
			l.record(frame, out, stats, catalog)
		}
		return out
	})
}

func (l *lazyConnectCursor) Close() error {
	return l.src.Close()
}

func (l *lazyConnectCursor) record(frame, out []byte, stats RewriteStats, catalog *CatalogShape) {
	l.s.events.Update(l.seq, func(e *Event) {
		if stats.Source != "" {
			e.Source = stats.Source
			c := stats.Confidence
			e.Confidence = &c
		}
		if stats.NeedsTool != 0 || stats.Source == sourceJev {
			n := stats.NeedsTool
			e.NeedsTool = &n
		}
		if stats.OriginalModel != "" {
			e.OriginalModel = stats.OriginalModel
		}
		if stats.SentModel != "" {
			e.SentModel = stats.SentModel
		}
		if stats.Reason != "" && (e.Reason == "" || e.Reason == reasonStream || e.Reason == reasonNotChat) {
			e.Reason = stats.Reason
		}
		if stats.Apply == applyFilter || stats.Apply == applyForced || e.Apply == "" || e.Apply == applyNone {
			if stats.Apply != "" {
				e.Apply = stats.Apply
			}
		}
		if stats.Chosen != "" {
			e.Chosen = stats.Chosen
		}
		e.Changed = e.Changed || stats.Changed
		if stats.ToolBefore > e.ToolBefore {
			e.ToolBefore = stats.ToolBefore
		}
		if stats.ToolAfter > 0 {
			e.ToolAfter = stats.ToolAfter
		}
		e.CompactApplied = e.CompactApplied || stats.CompactApplied
		e.ReasoningChanged = e.ReasoningChanged || stats.ReasoningChanged
		if stats.CompactDropped > e.CompactDropped {
			e.CompactDropped = stats.CompactDropped
		}
		e.Catalog = mergeDevinCatalog(e.Catalog, catalog)
		if stats.Protocol != "" {
			e.Protocol = stats.Protocol
		}
	})
	l.s.applyConnectCursorStats(stats, len(frame), len(out))
	if l.s.Log != nil {
		l.s.Log.Print(FormatStats(stats))
	}
}

func (s *Server) applyConnectCursorStats(stats RewriteStats, before, after int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Last = stats
	if !stats.Changed {
		return
	}
	if s.Passthrough > 0 {
		s.Passthrough--
	}
	s.Rewritten++
	if stats.Apply != "" && stats.Apply != applyNone {
		s.SelectionApplied++
		if s.SelectionSources == nil {
			s.SelectionSources = map[string]int{}
		}
		if s.ApplicationModes == nil {
			s.ApplicationModes = map[string]int{}
		}
		s.SelectionSources[stats.Source]++
		s.ApplicationModes[stats.Apply]++
	}
	if stats.CompactApplied {
		s.CompactionApplied++
	}
	s.CharsBefore += before
	s.CharsAfter += after
}

func readConnectFrame(r io.Reader) ([]byte, bool) {
	hdr := make([]byte, 5)
	n, _ := io.ReadFull(r, hdr)
	if n == 0 {
		return nil, false
	}
	if n < 5 {
		return hdr[:n], false
	}
	ln := binary.BigEndian.Uint32(hdr[1:5])
	if ln > maxRequestBodyBytes {
		return hdr, false
	}
	if ln == 0 {
		return hdr, true
	}
	payload := make([]byte, ln)
	got, _ := io.ReadFull(r, payload)
	if uint32(got) < ln {
		return append(hdr, payload[:got]...), false
	}
	return append(hdr, payload...), true
}

func observeConnectFrame(events *EventLog, seq int64, catalog *CatalogShape) {
	events.Update(seq, func(e *Event) {
		e.ConnectFrames++
		if catalog != nil {
			e.Catalog = mergeDevinCatalog(e.Catalog, catalog)
		}
	})
}

// emitConnectFrame copies one rewritten Connect frame into p.
// When buf is empty it reads the next 5+len envelope from src.
// Incomplete trailing bytes pass through unchanged.
func emitConnectFrame(p []byte, buf *[]byte, sticky *error, src io.Reader, rewrite func([]byte) []byte) (int, error) {
	if len(*buf) == 0 {
		if *sticky != nil {
			return 0, *sticky
		}
		frame, complete := readConnectFrame(src)
		if !complete {
			*buf = frame
			*sticky = io.EOF
			if len(*buf) == 0 {
				return 0, io.EOF
			}
		} else {
			out := rewrite(frame)
			if len(out) == 0 {
				out = frame
			}
			*buf = out
		}
	}
	n := copy(p, *buf)
	*buf = (*buf)[n:]
	if len(*buf) == 0 {
		return n, *sticky
	}
	return n, nil
}

func rewriteConnectCursorFrame(ctx context.Context, frame []byte, h host.ID, client *jev.Client, opt Options) ([]byte, RewriteStats, *CatalogShape, bool) {
	var stats RewriteStats
	if len(frame) < 5 {
		return frame, stats, nil, false
	}
	flags := frame[0]
	payload := frame[5:]
	raw := payload
	if flags&connectFlagCompressed != 0 {
		gr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return frame, stats, nil, false
		}
		dec, err := io.ReadAll(gr)
		_ = gr.Close()
		if err != nil {
			return frame, stats, nil, false
		}
		raw = dec
	}
	req, wrapField, ok := unwrapAgentRun(raw)
	if !ok {
		return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
	}
	lift, ok := liftCursorAgent(req)
	if !ok {
		return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
	}
	lifted, err := json.Marshal(lift.root)
	if err != nil {
		return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
	}
	rewritten, stats, err := RewriteWith(ctx, lifted, h, client, opt)
	if err != nil {
		return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
	}
	if opt.AfterRewrite != nil {
		rewritten = opt.AfterRewrite(rewritten)
	}
	shape := mergeDevinCatalog(catalogShape(lifted), protoFieldCatalog(req))
	if !stats.Changed && bytes.Equal(rewritten, lifted) {
		if out, cstats, catalog, applied := rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt); applied && (cstats.CompactApplied || cstats.Changed) {
			return out, cstats, catalog, applied
		}
		return frame, stats, shape, false
	}
	var next map[string]any
	if err := json.Unmarshal(rewritten, &next); err != nil {
		return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
	}
	newReq := writeAgentRunRequest(req, next, lift.toolBytes, lift.histItems, lift.field1Items)
	newRaw := newReq
	if wrapField > 0 {
		newRaw = replaceLengthField(raw, wrapField, newReq)
	}
	newPayload := newRaw
	if flags&connectFlagCompressed != 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(newRaw); err != nil {
			_ = zw.Close()
			return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
		}
		if err := zw.Close(); err != nil {
			return rewriteConnectCursorExec(ctx, frame, flags, raw, h, client, opt)
		}
		newPayload = buf.Bytes()
	}
	return connectFrame(flags, newPayload), stats, shape, true
}

const (
	execResultTextLimit = 400
	execResultKeepRunes = 200
)

func rewriteConnectCursorExec(ctx context.Context, frame []byte, flags byte, raw []byte, h host.ID, client *jev.Client, opt Options) ([]byte, RewriteStats, *CatalogShape, bool) {
	catalog := protoFieldCatalog(raw)
	var stats RewriteStats
	outRaw := raw
	if bag := liftExecRequestContext(raw); len(bag.tools) > 0 {
		task := cursorTaskFrom(ctx)
		if task == "" {
			task = deepRequestContextTask(firstLD(firstLD(raw, 2), 10))
		}
		root := map[string]any{"mcpTools": map[string]any{"mcpTools": bag.tools}}
		if task != "" {
			root["messages"] = []any{map[string]any{"role": "user", "content": task}}
		}
		if lifted, err := json.Marshal(root); err == nil {
			rewritten, st, rerr := RewriteWith(ctx, lifted, h, client, opt)
			if rerr == nil {
				if opt.AfterRewrite != nil {
					rewritten = opt.AfterRewrite(rewritten)
				}
				catalog = mergeDevinCatalog(catalogShape(lifted), catalog)
				if st.Changed {
					var next map[string]any
					if json.Unmarshal(rewritten, &next) == nil {
						if updated, ok := writebackExecRequestContext(outRaw, jsonToolNames(next), bag); ok {
							outRaw = updated
							stats = st
						}
					}
				} else {
					stats = st
				}
			}
		}
	}
	if opt.Compaction != CompactionOff {
		if compacted, cstats, ok := compactExecClientResult(outRaw); ok {
			outRaw = compacted
			stats.Changed = true
			stats.CompactApplied = true
			stats.CharsBefore += cstats.CharsBefore
			stats.CharsAfter += cstats.CharsAfter
			if stats.Protocol == "" {
				stats.Protocol = cstats.Protocol
			}
		}
	}
	if !stats.Changed {
		return frame, stats, catalog, false
	}
	newPayload := outRaw
	if flags&connectFlagCompressed != 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(outRaw); err != nil {
			_ = zw.Close()
			return frame, RewriteStats{}, catalog, false
		}
		if err := zw.Close(); err != nil {
			return frame, RewriteStats{}, catalog, false
		}
		newPayload = buf.Bytes()
	}
	return connectFrame(flags, newPayload), stats, catalog, true
}

func compactExecClientResult(raw []byte) ([]byte, RewriteStats, bool) {
	var stats RewriteStats
	fields, ok := parseProtoFields(raw)
	if !ok {
		return raw, stats, false
	}
	var exec []byte
	for _, f := range fields {
		if f.field == 2 && f.wire == 2 {
			exec = f.raw
			break
		}
	}
	if exec == nil || looksWrappedAgentRun(exec) {
		return raw, stats, false
	}
	execFields, ok := parseProtoFields(exec)
	if !ok {
		return raw, stats, false
	}
	var bestPath []int
	var best []byte
	for _, f := range execFields {
		if f.wire != 2 || execIDField(f) {
			continue
		}
		path, text := longestProtoLikelyText(f.raw, []int{f.field})
		if len(text) > len(best) {
			best = text
			bestPath = path
		}
	}
	if len(best) <= execResultTextLimit || len(bestPath) == 0 {
		return raw, stats, false
	}
	stub := execResultStub(string(best))
	newExec := replaceNestedText(exec, bestPath, stub)
	if bytes.Equal(newExec, exec) {
		return raw, stats, false
	}
	stats.Changed = true
	stats.CompactApplied = true
	stats.CharsBefore = len(best)
	stats.CharsAfter = len(stub)
	stats.Protocol = "cursor"
	return replaceLengthField(raw, 2, newExec), stats, true
}

func looksWrappedAgentRun(b []byte) bool {
	fields, ok := parseProtoFields(b)
	if !ok {
		return false
	}
	has := map[int]bool{}
	for _, f := range fields {
		has[f.field] = true
	}
	if has[4] || has[13] || has[26] {
		return true
	}
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 && looksConversationState(f.raw) {
			return true
		}
	}
	return false
}

func execIDField(f protoField) bool {
	return protoLikelyLeafText(f.raw) && len(f.raw) <= execResultTextLimit
}

func longestProtoLikelyText(body []byte, prefix []int) ([]int, []byte) {
	if protoLikelyText(body) {
		return prefix, body
	}
	fields, ok := parseProtoFields(body)
	if !ok {
		return nil, nil
	}
	var bestPath []int
	var best []byte
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		path := append(append([]int{}, prefix...), f.field)
		if protoLikelyText(f.raw) {
			if len(f.raw) > len(best) {
				best = f.raw
				bestPath = path
			}
			continue
		}
		if subPath, sub := longestProtoLikelyText(f.raw, path); len(sub) > len(best) {
			best = sub
			bestPath = subPath
		}
	}
	return bestPath, best
}

func execResultStub(text string) string {
	rs := []rune(text)
	if len(rs) > execResultKeepRunes {
		rs = rs[:execResultKeepRunes]
	}
	return "jev-compaction truncated\n" + string(rs)
}

type protoField struct {
	field int
	wire  int
	raw   []byte
	full  []byte
}

func looksConnectFrame(frame []byte) bool {
	if len(frame) < 5 {
		return false
	}
	ln := int(frame[1])<<24 | int(frame[2])<<16 | int(frame[3])<<8 | int(frame[4])
	return frame[0] <= 0x03 && ln >= 0 && ln+5 == len(frame)
}

func connectFramePayload(frame []byte) []byte {
	body := frame
	if len(frame) >= 5 {
		body = frame[5:]
		if frame[0]&connectFlagCompressed != 0 {
			if gr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
				if dec, err := io.ReadAll(gr); err == nil {
					body = dec
				}
				_ = gr.Close()
			}
		}
	}
	unwrapped, _ := unwrapProtoPayload(body)
	return unwrapped
}

func unwrapProtoPayload(raw []byte) (payload []byte, gzipped bool) {
	payload = raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gzipped = true
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err == nil {
			dec, err := io.ReadAll(gr)
			_ = gr.Close()
			if err == nil {
				payload = dec
			}
		}
	}
	if _, ok := parseProtoFields(payload); ok {
		return payload, gzipped
	}
	if len(payload) > 5 {
		rest := payload[5:]
		if len(rest) >= 2 && rest[0] == 0x1f && rest[1] == 0x8b {
			gzipped = true
			gr, err := gzip.NewReader(bytes.NewReader(rest))
			if err == nil {
				dec, err := io.ReadAll(gr)
				_ = gr.Close()
				if err == nil {
					rest = dec
				}
			}
		}
		if _, ok := parseProtoFields(rest); ok {
			return rest, gzipped
		}
	}
	return payload, gzipped
}

func parseProtoFields(body []byte) ([]protoField, bool) {
	var fields []protoField
	i := 0
	for i < len(body) {
		start := i
		tag, n := protoVarint(body[i:])
		if n <= 0 {
			return nil, false
		}
		i += n
		field := int(tag >> 3)
		wire := int(tag & 7)
		switch wire {
		case 0:
			_, n = protoVarint(body[i:])
			if n <= 0 {
				return nil, false
			}
			raw := body[i : i+n]
			i += n
			fields = append(fields, protoField{field: field, wire: wire, raw: raw, full: body[start:i]})
		case 1:
			if i+8 > len(body) {
				return nil, false
			}
			raw := body[i : i+8]
			i += 8
			fields = append(fields, protoField{field: field, wire: wire, raw: raw, full: body[start:i]})
		case 2:
			ln, n := protoVarint(body[i:])
			if n <= 0 || i+n+int(ln) > len(body) {
				return nil, false
			}
			i += n
			raw := body[i : i+int(ln)]
			i += int(ln)
			fields = append(fields, protoField{field: field, wire: wire, raw: raw, full: body[start:i]})
		case 5:
			if i+4 > len(body) {
				return nil, false
			}
			raw := body[i : i+4]
			i += 4
			fields = append(fields, protoField{field: field, wire: wire, raw: raw, full: body[start:i]})
		default:
			return nil, false
		}
	}
	return fields, true
}

func firstLD(body []byte, field int) []byte {
	fields, ok := parseProtoFields(body)
	if !ok {
		return nil
	}
	for _, f := range fields {
		if f.field == field && f.wire == 2 {
			return f.raw
		}
	}
	return nil
}

func looksConversationState(b []byte) bool {
	fields, ok := parseProtoFields(b)
	if !ok {
		return false
	}
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 && (json.Valid(f.raw) || looksProtoUserMessage(f.raw)) {
			return true
		}
	}
	return false
}

func looksProtoUserMessage(b []byte) bool {
	fields, ok := parseProtoFields(b)
	if !ok {
		return false
	}
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 && protoLikelyLeafText(f.raw) && !json.Valid(f.raw) {
			return true
		}
	}
	return false
}

func looksAgentRun(b []byte) bool {
	fields, ok := parseProtoFields(b)
	if !ok || len(fields) == 0 {
		return false
	}
	has := map[int]bool{}
	for _, f := range fields {
		has[f.field] = true
	}
	if has[2] || has[4] || has[5] || has[13] || has[26] {
		return true
	}
	if !has[1] {
		return false
	}
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 && looksConversationState(f.raw) {
			return true
		}
	}
	return false
}

func unwrapAgentRun(payload []byte) ([]byte, int, bool) {
	fields, ok := parseProtoFields(payload)
	if ok {
		nested := map[int][]byte{}
		var actionRaw []byte
		hasField1 := false
		for _, f := range fields {
			if f.wire != 2 {
				continue
			}
			if f.field == 1 {
				hasField1 = true
			}
			if f.field == 4 && looksConversationAction(f.raw) {
				actionRaw = f.raw
			}
			switch f.field {
			case 1, 2, 4, 5:
				if _, exists := nested[f.field]; !exists {
					nested[f.field] = f.raw
				}
			}
		}
		if raw, ok := nested[1]; ok && looksAgentRun(raw) {
			return raw, 1, true
		}
		for _, n := range []int{2, 4, 5} {
			raw, ok := nested[n]
			if !ok || !looksAgentRun(raw) || looksConversationAction(raw) {
				continue
			}
			return raw, n, true
		}
		if actionRaw != nil && !hasField1 {
			return actionRaw, 4, true
		}
	}
	if looksAgentRun(payload) {
		return payload, 0, true
	}
	return nil, 0, false
}

func looksConversationAction(b []byte) bool {
	if conversationActionText(b) == "" {
		return false
	}
	fields, ok := parseProtoFields(b)
	if !ok {
		return false
	}
	for _, f := range fields {
		switch f.field {
		case 4, 5, 13, 26:
			return false
		}
	}
	return true
}

func conversationActionText(b []byte) string {
	uma := firstLD(b, 1)
	if uma == nil {
		return ""
	}
	um := firstLD(uma, 1)
	if um == nil {
		return ""
	}
	if t := firstLD(um, 1); protoLikelyLeafText(t) {
		return string(t)
	}
	return ""
}

func liftAgentRunJSON(req []byte) (map[string]any, map[string][]byte, bool) {
	lift, ok := liftCursorAgent(req)
	return lift.root, lift.toolBytes, ok
}

func liftCursorAgent(req []byte) (cursorAgentLift, bool) {
	if looksConversationAction(req) {
		if action := liftAction(req); action != nil {
			root := map[string]any{"action": action}
			lift := cursorAgentLift{root: root}
			if tools, tb := liftActionRequestContextTools(req); len(tools) > 0 {
				root["mcpTools"] = map[string]any{"mcpTools": tools}
				lift.toolBytes = tb
			}
			return lift, true
		}
	}
	fields, ok := parseProtoFields(req)
	if !ok {
		return cursorAgentLift{}, false
	}
	root := map[string]any{}
	toolBytes := map[string][]byte{}
	var histItems []cursorTurnItem
	var field1Items []cursorTurnItem
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		switch f.field {
		case 1:
			raw, _ := unwrapProtoPayload(f.raw)
			msgs := liftJSONStrings(raw, 1)
			turnMsgs, items := liftConversationTurnItems(raw)
			msgs = append(msgs, liftConversationTurns(raw)...)
			if len(msgs) > 0 {
				root["conversationState"] = map[string]any{"rootPromptMessagesJson": msgs}
			}
			field1Msgs, field1Kept := liftConversationStateField1(raw)
			if len(field1Msgs) > 0 {
				root["messages"] = field1Msgs
				field1Items = field1Kept
			} else if !conversationStateField1AllJSON(raw) && len(turnMsgs) > 0 {
				root["messages"] = turnMsgs
				histItems = items
			}
		case 2:
			if action := liftAction(f.raw); action != nil {
				root["action"] = action
			}
			if tools, tb := liftActionRequestContextTools(f.raw); len(tools) > 0 {
				root["mcpTools"] = map[string]any{"mcpTools": tools}
				for k, v := range tb {
					toolBytes[k] = v
				}
			}
		case 4:
			raw, _ := unwrapProtoPayload(f.raw)
			tools, tb := liftMcpTools(raw)
			for k, v := range tb {
				toolBytes[k] = v
			}
			if len(tools) > 0 {
				root["mcpTools"] = map[string]any{"mcpTools": tools}
			}
		case 5:
			root["conversationId"] = string(f.raw)
		case 13:
			root["harness"] = string(f.raw)
		case 26:
			root["agentSessionId"] = string(f.raw)
		}
	}
	if text := cursorLatestUserText(req); text != "" {
		if _, ok := root["action"]; !ok {
			root["action"] = map[string]any{
				"userMessageAction": map[string]any{
					"userMessage": map[string]any{"text": text},
				},
			}
		}
		if !cursorRootHasUserText(root, text) {
			root["messages"] = append(asSlice(root["messages"]), map[string]any{"role": "user", "content": text})
		}
	}
	return cursorAgentLift{root: root, toolBytes: toolBytes, histItems: histItems, field1Items: field1Items}, true
}

func cursorLatestUserText(req []byte) string {
	if text := liftActionText(req); text != "" {
		return text
	}
	return deepRequestContextTask(req)
}

func cursorRootHasUserText(root map[string]any, text string) bool {
	if text == "" || root == nil {
		return false
	}
	if action, _ := root["action"].(map[string]any); userFromAction(action) == text {
		return true
	}
	for _, m := range asSlice(root["messages"]) {
		obj, _ := m.(map[string]any)
		if obj == nil {
			continue
		}
		if s, _ := obj["content"].(string); s == text {
			return true
		}
	}
	return false
}

func liftJSONStrings(msg []byte, field int) []any {
	fields, ok := parseProtoFields(msg)
	if !ok {
		return nil
	}
	var out []any
	for _, f := range fields {
		if f.field == field && f.wire == 2 {
			out = append(out, liftPromptJSON(f.raw)...)
		}
	}
	return out
}

func liftPromptJSON(raw []byte) []any {
	if msgs := liftJSONMessages(raw); len(msgs) > 0 {
		return msgs
	}
	if msgs := liftProtoUserMessage(raw, 0); len(msgs) > 0 {
		return msgs
	}
	return []any{string(raw)}
}

func liftProtoUserMessage(raw []byte, depth int) []any {
	if depth > 1 {
		return nil
	}
	fields, ok := parseProtoFields(raw)
	if !ok {
		return nil
	}
	if out := liftFromField1(fields); len(out) > 0 {
		return out
	}
	if depth >= 1 {
		return nil
	}
	var out []any
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		out = append(out, liftProtoUserMessage(f.raw, depth+1)...)
	}
	return out
}

func liftFromField1(fields []protoField) []any {
	var out []any
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		if msgs := liftJSONMessages(f.raw); len(msgs) > 0 {
			out = append(out, msgs...)
			continue
		}
		if protoLikelyLeafText(f.raw) {
			msg, err := json.Marshal(map[string]any{"role": "user", "content": string(f.raw)})
			if err == nil {
				out = append(out, string(msg))
			}
		}
	}
	return out
}

func liftJSONMessages(raw []byte) []any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) == nil && obj != nil {
			return []any{string(trimmed)}
		}
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(trimmed, &arr) != nil {
			return nil
		}
		var out []any
		for _, el := range arr {
			el = bytes.TrimSpace(el)
			if len(el) > 0 && el[0] == '{' {
				out = append(out, string(el))
			}
		}
		return out
	}
	return nil
}

func liftConversationTurns(state []byte) []any {
	msgs, _ := liftConversationTurnItems(state)
	var out []any
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cp := cloneMap(m)
		delete(cp, "_idx")
		b, err := json.Marshal(cp)
		if err != nil {
			continue
		}
		out = append(out, string(b))
	}
	return out
}

func liftConversationTurnItems(state []byte) ([]any, []cursorTurnItem) {
	state, _ = unwrapProtoPayload(state)
	fields, ok := parseProtoFields(state)
	if !ok {
		return nil, nil
	}
	var msgs []any
	var items []cursorTurnItem
	slot := 0
	for _, f := range fields {
		if f.field != 8 || f.wire != 2 {
			continue
		}
		idx := slot
		slot++
		msg, ok := liftCursorTurnMessage(f.raw)
		if !ok {
			continue
		}
		msg["_idx"] = idx
		msgs = append(msgs, msg)
		items = append(items, cursorTurnItem{idx: idx, raw: append([]byte(nil), f.raw...), msg: msg})
	}
	return msgs, items
}

func liftConversationStateField1(state []byte) ([]any, []cursorTurnItem) {
	state, _ = unwrapProtoPayload(state)
	fields, ok := parseProtoFields(state)
	if !ok {
		return nil, nil
	}
	var msgs []any
	var items []cursorTurnItem
	slot := 0
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		idx := slot
		slot++
		parsed, _ := parsePromptMessages(liftPromptJSON(f.raw))
		if len(parsed) == 0 && protoLikelyLeafText(f.raw) {
			parsed = []any{map[string]any{"role": "user", "content": string(f.raw)}}
		}
		if len(parsed) == 0 {
			continue
		}
		var first map[string]any
		for _, p := range parsed {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			m["_idx"] = idx
			msgs = append(msgs, m)
			if first == nil {
				first = m
			}
		}
		if first != nil {
			items = append(items, cursorTurnItem{idx: idx, raw: append([]byte(nil), f.raw...), msg: first})
		}
	}
	return msgs, items
}

func liftCursorTurnMessage(turn []byte) (map[string]any, bool) {
	if um := firstLD(turn, 1); len(um) > 0 {
		if msg, ok := liftCursorUserOrJSON(um); ok {
			return msg, true
		}
	}
	fields, ok := parseProtoFields(turn)
	if !ok {
		return nil, false
	}
	for _, f := range fields {
		if f.field != 2 || f.wire != 2 {
			continue
		}
		if msg, ok := liftCursorStepMessage(f.raw); ok {
			return msg, true
		}
	}
	return nil, false
}

func liftCursorUserOrJSON(raw []byte) (map[string]any, bool) {
	if msg, ok := jsonBytesToMsg(raw); ok {
		return msg, true
	}
	if inner := firstLD(raw, 1); len(inner) > 0 {
		if msg, ok := jsonBytesToMsg(inner); ok {
			return msg, true
		}
		if text := firstLD(inner, 1); protoLikelyText(text) {
			return map[string]any{"role": "user", "content": string(text)}, true
		}
		if protoLikelyText(inner) {
			return map[string]any{"role": "user", "content": string(inner)}, true
		}
	}
	if protoLikelyLeafText(raw) {
		return map[string]any{"role": "user", "content": string(raw)}, true
	}
	if msgs := liftProtoUserMessage(raw, 0); len(msgs) > 0 {
		return jsonBytesToMsg([]byte(fmtString(msgs[0])))
	}
	return nil, false
}

func liftCursorStepMessage(raw []byte) (map[string]any, bool) {
	if msg, ok := jsonBytesToMsg(raw); ok {
		if _, has := msg["role"]; !has {
			msg["role"] = "assistant"
		}
		return msg, true
	}
	if inner := firstLD(raw, 1); len(inner) > 0 {
		if msg, ok := jsonBytesToMsg(inner); ok {
			if _, has := msg["role"]; !has {
				msg["role"] = "assistant"
			}
			return msg, true
		}
		if protoLikelyText(inner) {
			return map[string]any{"role": "assistant", "content": string(inner)}, true
		}
	}
	if protoLikelyLeafText(raw) {
		return map[string]any{"role": "assistant", "content": string(raw)}, true
	}
	return nil, false
}

func jsonBytesToMsg(raw []byte) (map[string]any, bool) {
	for _, el := range liftJSONMessages(raw) {
		switch v := el.(type) {
		case map[string]any:
			return v, true
		case string:
			var m map[string]any
			if json.Unmarshal([]byte(v), &m) == nil && m != nil {
				return m, true
			}
		}
	}
	return nil, false
}

func fmtString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func protoLikelyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

func protoLikelyLeafText(b []byte) bool {
	if !protoLikelyText(b) {
		return false
	}
	fields, ok := parseProtoFields(b)
	if !ok || len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if f.wire == 2 {
			return false
		}
	}
	return true
}

func liftMcpTools(msg []byte) ([]any, map[string][]byte) {
	fields, ok := parseProtoFields(msg)
	if !ok {
		return nil, nil
	}
	var tools []any
	tb := map[string][]byte{}
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		name, desc := mcpToolNameDesc(f.raw)
		if name == "" {
			continue
		}
		tb[name] = append([]byte(nil), f.raw...)
		tools = append(tools, map[string]any{"name": name, "description": desc})
	}
	return tools, tb
}

func mcpToolNameDesc(def []byte) (name, desc string) {
	fields, ok := parseProtoFields(def)
	if !ok {
		return "", ""
	}
	var texts []string
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		if f.field == 1 {
			name = mcpToolField1Name(f.raw)
		}
		if f.field == 2 && desc == "" && protoLikelyText(f.raw) {
			desc = string(f.raw)
		}
		collectToolIdentTexts(f.raw, &texts)
	}
	if isToolIdentName(name) {
		return name, desc
	}
	best := ""
	for _, s := range texts {
		if !isToolIdentName(s) {
			continue
		}
		if best == "" || len(s) > len(best) {
			best = s
		}
	}
	return best, desc
}

func mcpToolField1Name(raw []byte) string {
	s := string(raw)
	if isToolIdentName(s) {
		return s
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) == nil {
			for _, k := range []string{"name", "tool_name"} {
				v, _ := obj[k].(string)
				if v != "" {
					return v
				}
			}
		}
	}
	if inner := firstLD(raw, 1); protoLikelyText(inner) && isToolIdentName(string(inner)) {
		return string(inner)
	}
	return ""
}

func collectToolIdentTexts(raw []byte, texts *[]string) {
	if protoLikelyText(raw) {
		*texts = append(*texts, string(raw))
	}
	fields, ok := parseProtoFields(raw)
	if !ok {
		return
	}
	for _, f := range fields {
		if f.wire == 2 && protoLikelyText(f.raw) {
			*texts = append(*texts, string(f.raw))
		}
	}
}

func liftAction(raw []byte) map[string]any {
	uma := firstLD(raw, 1)
	if uma == nil {
		return nil
	}
	text := ""
	if um := firstLD(uma, 1); um != nil {
		if t := firstLD(um, 1); t != nil {
			text = string(t)
		}
	}
	return map[string]any{
		"userMessageAction": map[string]any{
			"userMessage": map[string]any{"text": text},
		},
	}
}

type cursorTaskKey struct{}

func cursorTaskFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(cursorTaskKey{}).(string)
	return s
}

func cursorTaskFromFrame(frame []byte) string {
	if len(frame) < 5 {
		return ""
	}
	raw := frame[5:]
	if frame[0]&connectFlagCompressed != 0 {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return ""
		}
		dec, err := io.ReadAll(gr)
		_ = gr.Close()
		if err != nil {
			return ""
		}
		raw = dec
	}
	if req, _, ok := unwrapAgentRun(raw); ok {
		if text := liftActionText(req); text != "" {
			return text
		}
	}
	return liftActionText(raw)
}

func requestContextTask(rc []byte) string {
	if t := firstLD(rc, 21); protoLikelyLeafText(t) {
		return string(t)
	}
	return ""
}

func deepRequestContextTask(msg []byte) string {
	if t := requestContextTask(msg); t != "" {
		return t
	}
	fields, ok := parseProtoFields(msg)
	if !ok {
		return ""
	}
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		if t := deepRequestContextTask(f.raw); t != "" {
			return t
		}
	}
	return ""
}

type protoToolBag struct {
	path  []int
	field int
	tools []any
	bytes map[string][]byte
}

func bestToolBag(msg []byte, path []int, depth int) protoToolBag {
	if depth > 6 || len(msg) == 0 {
		return protoToolBag{}
	}
	raw := msg
	if dec, _ := unwrapProtoPayload(msg); len(dec) > 0 {
		raw = dec
	}
	fields, ok := parseProtoFields(raw)
	if !ok {
		return protoToolBag{}
	}
	byField := map[int][]protoField{}
	for _, f := range fields {
		if f.wire == 2 {
			byField[f.field] = append(byField[f.field], f)
		}
	}
	best := protoToolBag{}
	for field, items := range byField {
		var tools []any
		tb := map[string][]byte{}
		for _, it := range items {
			name, desc := mcpToolNameDesc(it.raw)
			if name == "" {
				continue
			}
			tb[name] = append([]byte(nil), it.raw...)
			tools = append(tools, map[string]any{"name": name, "description": desc})
		}
		if len(tools) > len(best.tools) {
			best = protoToolBag{path: append([]int(nil), path...), field: field, tools: tools, bytes: tb}
		}
	}
	if len(best.tools) >= 2 {
		return best
	}
	for field, items := range byField {
		childPath := append(append([]int(nil), path...), field)
		for _, it := range items {
			if inner := bestToolBag(it.raw, childPath, depth+1); len(inner.tools) > len(best.tools) {
				best = inner
			}
		}
	}
	return best
}

func replaceAtPath(root []byte, path []int, field int, raws [][]byte) []byte {
	if len(path) == 0 {
		return replaceRepeatedBytesField(root, field, raws)
	}
	head, rest := path[0], path[1:]
	cur := firstLD(root, head)
	if cur == nil {
		return root
	}
	return replaceLengthField(root, head, replaceAtPath(cur, rest, field, raws))
}

func dedupeNamedTools(tools []any, tb map[string][]byte) ([]any, map[string][]byte) {
	seen := map[string]bool{}
	out := make([]any, 0, len(tools))
	next := map[string][]byte{}
	for _, raw := range tools {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, raw)
		if b, ok := tb[name]; ok {
			next[name] = b
		}
	}
	return out, next
}

func liftActionRequestContextTools(action []byte) ([]any, map[string][]byte) {
	uma := firstLD(action, 1)
	if uma == nil {
		uma = action
	}
	rc := firstLD(uma, 2)
	bag := bestToolBag(rc, nil, 0)
	if len(bag.tools) == 0 {
		bag = bestToolBag(action, nil, 0)
	}
	return dedupeNamedTools(bag.tools, bag.bytes)
}

func liftExecRequestContext(raw []byte) protoToolBag {
	exec := firstLD(raw, 2)
	if exec == nil || looksWrappedAgentRun(exec) {
		return protoToolBag{}
	}
	bag := bestToolBag(firstLD(exec, 10), nil, 0)
	bag.tools, bag.bytes = dedupeNamedTools(bag.tools, bag.bytes)
	return bag
}

func writebackActionRequestContextTools(action []byte, names []string, toolBytes map[string][]byte) ([]byte, bool) {
	uma := firstLD(action, 1)
	wrapped := uma != nil
	root := action
	if wrapped {
		root = uma
	}
	scan := firstLD(root, 2)
	if scan == nil {
		scan = root
	}
	bag := bestToolBag(scan, nil, 0)
	if len(bag.tools) == 0 {
		return action, false
	}
	newScan := replaceAtPath(scan, bag.path, bag.field, keptToolRaws(names, toolBytes))
	if firstLD(root, 2) != nil {
		newRoot := replaceLengthField(root, 2, newScan)
		if !wrapped {
			return newRoot, true
		}
		return replaceLengthField(action, 1, newRoot), true
	}
	if !wrapped {
		return newScan, true
	}
	return replaceLengthField(action, 1, newScan), true
}

func writebackExecRequestContext(raw []byte, names []string, bag protoToolBag) ([]byte, bool) {
	if len(bag.tools) == 0 || bag.field == 0 {
		return raw, false
	}
	exec := firstLD(raw, 2)
	rcr := firstLD(exec, 10)
	if rcr == nil {
		return raw, false
	}
	newRCR := replaceAtPath(rcr, bag.path, bag.field, keptToolRaws(names, bag.bytes))
	newExec := replaceLengthField(exec, 10, newRCR)
	return replaceLengthField(raw, 2, newExec), true
}

func liftActionText(req []byte) string {
	action := firstLD(req, 2)
	if action == nil {
		return ""
	}
	uma := firstLD(action, 1)
	if uma == nil {
		return ""
	}
	um := firstLD(uma, 1)
	if um == nil {
		return ""
	}
	if t := firstLD(um, 1); t != nil {
		return string(t)
	}
	return ""
}

func jsonActionText(root map[string]any) string {
	action, _ := root["action"].(map[string]any)
	if action == nil {
		return ""
	}
	return userFromAction(action)
}

func jsonHistStrings(root map[string]any) []string {
	state, _ := root["conversationState"].(map[string]any)
	if state == nil {
		state, _ = root["conversation_state"].(map[string]any)
	}
	if state == nil {
		return nil
	}
	raw := asSlice(state["rootPromptMessagesJson"])
	if raw == nil {
		raw = asSlice(state["root_prompt_messages_json"])
	}
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, el := range raw {
		switch v := el.(type) {
		case string:
			out = append(out, v)
		default:
			b, err := json.Marshal(v)
			if err == nil {
				out = append(out, string(b))
			}
		}
	}
	return out
}

func conversationStateField1AllJSON(state []byte) bool {
	fields, ok := parseProtoFields(state)
	if !ok {
		return false
	}
	saw := false
	for _, f := range fields {
		if f.field != 1 || f.wire != 2 {
			continue
		}
		saw = true
		if len(liftJSONMessages(f.raw)) == 0 {
			return false
		}
	}
	return saw
}

func jsonToolNames(root map[string]any) []string {
	var names []string
	for _, d := range cursorToolDefs(root) {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if n := toolNameOf(m); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func writeAgentRunRequest(orig []byte, root map[string]any, toolBytes map[string][]byte, histItems, field1Items []cursorTurnItem) []byte {
	fields, ok := parseProtoFields(orig)
	if !ok {
		return orig
	}
	hist := jsonHistStrings(root)
	names := jsonToolNames(root)
	origText := liftActionText(orig)
	newText := jsonActionText(root)
	var out []byte
	for _, f := range fields {
		switch {
		case f.field == 1 && f.wire == 2 && len(field1Items) > 0:
			if next, ok := writebackCursorField1(f.raw, field1Items, root); ok {
				out = append(out, protoBytes(1, next)...)
				break
			}
			out = append(out, f.full...)
		case f.field == 1 && f.wire == 2 && hist != nil && conversationStateField1AllJSON(f.raw):
			out = append(out, protoBytes(1, replaceRepeatedStringField(f.raw, 1, hist))...)
		case f.field == 1 && f.wire == 2 && len(histItems) > 0:
			if next, ok := writebackCursorTurns(f.raw, histItems, root); ok {
				out = append(out, protoBytes(1, next)...)
				break
			}
			out = append(out, f.full...)
		case f.field == 2 && f.wire == 2:
			nextAction := f.raw
			if newText != "" && newText != origText {
				nextAction = replaceNestedText(nextAction, []int{1, 1, 1}, newText)
			}
			if len(names) > 0 {
				if updated, ok := writebackActionRequestContextTools(nextAction, names, toolBytes); ok {
					nextAction = updated
				}
			}
			out = append(out, protoBytes(2, nextAction)...)
		case f.field == 4 && f.wire == 2 && len(names) > 0:
			out = append(out, protoBytes(4, replaceRepeatedBytesField(f.raw, 1, keptToolRaws(names, toolBytes)))...)
		case f.field == 5 && f.wire == 2:
			if s, ok := root["conversationId"].(string); ok {
				out = append(out, protoString(5, s)...)
				break
			}
			out = append(out, f.full...)
		case f.field == 13 && f.wire == 2:
			if s, ok := root["harness"].(string); ok {
				out = append(out, protoString(13, s)...)
				break
			}
			out = append(out, f.full...)
		case f.field == 26 && f.wire == 2:
			if s, ok := root["agentSessionId"].(string); ok {
				out = append(out, protoString(26, s)...)
				break
			}
			out = append(out, f.full...)
		default:
			out = append(out, f.full...)
		}
	}
	return out
}

func writebackCursorTurns(state []byte, items []cursorTurnItem, next map[string]any) ([]byte, bool) {
	remaining := asSlice(next["messages"])
	if len(items) == 0 {
		return state, false
	}
	fields, ok := parseProtoFields(state)
	if !ok {
		return state, false
	}
	originals := make([][]byte, 0, len(fields))
	for _, f := range fields {
		if f.field == 8 && f.wire == 2 {
			originals = append(originals, f.raw)
		}
	}
	if len(originals) == 0 {
		return state, false
	}
	used := make([]bool, len(items))
	remainingByIdx := make(map[int]map[string]any, len(remaining))
	for _, remRaw := range remaining {
		rem, ok := remRaw.(map[string]any)
		if !ok {
			return state, false
		}
		if idx, ok := devinMsgIdx(rem); ok {
			remainingByIdx[idx] = rem
			continue
		}
		item, i, ok := matchCursorTurnRemaining(items, used, rem)
		if !ok {
			return state, false
		}
		used[i] = true
		remainingByIdx[item.idx] = rem
	}
	liftedByIdx := make(map[int]cursorTurnItem, len(items))
	for _, item := range items {
		liftedByIdx[item.idx] = item
	}
	out := make([][]byte, 0, len(originals))
	changed := false
	const stub = "jev-compaction truncated"
	for slot, orig := range originals {
		item, wasLifted := liftedByIdx[slot]
		rem, stillRemaining := remainingByIdx[slot]
		switch {
		case !wasLifted:
			out = append(out, orig)
		case stillRemaining:
			itemRaw := orig
			if newText, trunc := cursorTurnTruncated(item, rem); trunc {
				itemRaw = rewriteCursorTurnText(itemRaw, newText)
				if !bytes.Equal(itemRaw, orig) {
					changed = true
				}
			}
			out = append(out, itemRaw)
		default:
			itemRaw := rewriteCursorTurnText(orig, stub)
			if !bytes.Equal(itemRaw, orig) {
				changed = true
			}
			out = append(out, itemRaw)
		}
	}
	if !changed || len(out) != len(originals) {
		return state, false
	}
	return replaceRepeatedBytesField(state, 8, out), true
}

func writebackCursorField1(state []byte, items []cursorTurnItem, next map[string]any) ([]byte, bool) {
	remaining := asSlice(next["messages"])
	if len(items) == 0 {
		return state, false
	}
	fields, ok := parseProtoFields(state)
	if !ok {
		return state, false
	}
	originals := make([][]byte, 0, len(fields))
	for _, f := range fields {
		if f.field == 1 && f.wire == 2 {
			originals = append(originals, f.raw)
		}
	}
	if len(originals) == 0 {
		return state, false
	}
	used := make([]bool, len(items))
	remainingByIdx := make(map[int][]map[string]any, len(remaining))
	for _, remRaw := range remaining {
		rem, ok := remRaw.(map[string]any)
		if !ok {
			return state, false
		}
		if idx, ok := devinMsgIdx(rem); ok {
			remainingByIdx[idx] = append(remainingByIdx[idx], rem)
			continue
		}
		item, i, ok := matchCursorTurnRemaining(items, used, rem)
		if !ok {
			return state, false
		}
		used[i] = true
		remainingByIdx[item.idx] = append(remainingByIdx[item.idx], rem)
	}
	liftedByIdx := make(map[int]cursorTurnItem, len(items))
	for _, item := range items {
		liftedByIdx[item.idx] = item
	}
	out := make([][]byte, 0, len(originals))
	changed := false
	for slot, orig := range originals {
		item, wasLifted := liftedByIdx[slot]
		rems := remainingByIdx[slot]
		switch {
		case !wasLifted:
			out = append(out, orig)
		case len(rems) > 0:
			itemRaw := rewriteField1Remaining(orig, item, rems)
			if !bytes.Equal(itemRaw, orig) {
				changed = true
			}
			out = append(out, itemRaw)
		default:
			itemRaw := stubField1Item(orig)
			if !bytes.Equal(itemRaw, orig) {
				changed = true
			}
			out = append(out, itemRaw)
		}
	}
	if !changed || len(out) != len(originals) {
		return state, false
	}
	return replaceRepeatedBytesField(state, 1, out), true
}

func rewriteField1Remaining(orig []byte, item cursorTurnItem, rems []map[string]any) []byte {
	trimmed := bytes.TrimSpace(orig)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		arr := make([]any, 0, len(rems))
		for _, rem := range rems {
			arr = append(arr, stripCursorLiftMeta(rem))
		}
		b, err := json.Marshal(arr)
		if err != nil {
			return orig
		}
		return b
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		b, err := json.Marshal(stripCursorLiftMeta(rems[0]))
		if err != nil {
			return orig
		}
		return b
	}
	if newText, trunc := cursorTurnTruncated(item, rems[0]); trunc {
		if protoLikelyLeafText(orig) {
			return []byte(newText)
		}
		return rewriteCursorTurnText(orig, newText)
	}
	return orig
}

func stubField1Item(orig []byte) []byte {
	const stub = "jev-compaction truncated"
	trimmed := bytes.TrimSpace(orig)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		b, err := json.Marshal([]any{map[string]any{"role": "user", "content": stub}})
		if err == nil {
			return b
		}
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var m map[string]any
		if json.Unmarshal(trimmed, &m) == nil && m != nil {
			m["content"] = stub
			delete(m, "_idx")
			if b, err := json.Marshal(m); err == nil {
				return b
			}
		}
		b, err := json.Marshal(map[string]any{"role": "user", "content": stub})
		if err == nil {
			return b
		}
	}
	if protoLikelyLeafText(orig) {
		return []byte(stub)
	}
	return rewriteCursorTurnText(orig, stub)
}

func stripCursorLiftMeta(m map[string]any) map[string]any {
	out := cloneMap(m)
	delete(out, "_idx")
	return out
}

func matchCursorTurnRemaining(items []cursorTurnItem, used []bool, rem map[string]any) (cursorTurnItem, int, bool) {
	if idx, ok := devinMsgIdx(rem); ok {
		for i, item := range items {
			if used[i] || item.idx != idx {
				continue
			}
			return item, i, true
		}
	}
	return cursorTurnItem{}, -1, false
}

func cursorTurnTruncated(item cursorTurnItem, rem map[string]any) (string, bool) {
	before := textOf(item.msg)
	after := textOf(rem)
	if after == "" || before == "" || after == before {
		return "", false
	}
	if strings.Contains(after, "jev-compaction truncated") || len(after) < len(before) {
		return after, true
	}
	return "", false
}

func rewriteCursorTurnText(turn []byte, newText string) []byte {
	bestLen := -1
	_, _ = rewriteProtoStrings(turn, func(s string) (string, bool) {
		if protoLikelyText([]byte(s)) && len(s) > bestLen {
			bestLen = len(s)
		}
		return "", false
	})
	if bestLen < 0 {
		return turn
	}
	out, ok := rewriteProtoStrings(turn, func(s string) (string, bool) {
		if len(s) == bestLen && protoLikelyText([]byte(s)) {
			return newText, true
		}
		return "", false
	})
	if !ok {
		return turn
	}
	return out
}

func keptToolRaws(names []string, toolBytes map[string][]byte) [][]byte {
	var out [][]byte
	for _, name := range names {
		if raw, ok := toolBytes[name]; ok {
			out = append(out, raw)
		}
	}
	return out
}

func replaceRepeatedStringField(orig []byte, field int, strs []string) []byte {
	raws := make([][]byte, len(strs))
	for i, s := range strs {
		raws[i] = []byte(s)
	}
	return replaceRepeatedBytesField(orig, field, raws)
}

func replaceRepeatedBytesField(orig []byte, field int, raws [][]byte) []byte {
	fields, ok := parseProtoFields(orig)
	if !ok {
		return orig
	}
	var out []byte
	placed := false
	for _, f := range fields {
		if f.field == field && f.wire == 2 {
			if !placed {
				for _, raw := range raws {
					out = append(out, protoBytes(field, raw)...)
				}
				placed = true
			}
			continue
		}
		out = append(out, f.full...)
	}
	if !placed {
		for _, raw := range raws {
			out = append(out, protoBytes(field, raw)...)
		}
	}
	return out
}

func replaceLengthField(orig []byte, field int, raw []byte) []byte {
	fields, ok := parseProtoFields(orig)
	if !ok {
		return protoBytes(field, raw)
	}
	var out []byte
	replaced := false
	for _, f := range fields {
		if f.field == field && f.wire == 2 && !replaced {
			out = append(out, protoBytes(field, raw)...)
			replaced = true
			continue
		}
		out = append(out, f.full...)
	}
	if !replaced {
		out = append(protoBytes(field, raw), out...)
	}
	return out
}

func replaceNestedText(body []byte, path []int, text string) []byte {
	if len(path) == 0 {
		return []byte(text)
	}
	fields, ok := parseProtoFields(body)
	if !ok {
		return body
	}
	field := path[0]
	var out []byte
	replaced := false
	for _, f := range fields {
		if f.field == field && f.wire == 2 && !replaced {
			if len(path) == 1 {
				out = append(out, protoString(field, text)...)
			} else {
				out = append(out, protoBytes(field, replaceNestedText(f.raw, path[1:], text))...)
			}
			replaced = true
			continue
		}
		out = append(out, f.full...)
	}
	return out
}
