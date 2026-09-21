package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func wrapConnectDevinBody(body io.ReadCloser, s *Server, seq int64, ctx context.Context) io.ReadCloser {
	return &lazyConnectDevin{src: body, s: s, seq: seq, ctx: ctx}
}

// lazyConnectDevin rewrites each Connect envelope as Read pulls it
// without buffering the rest of the stream.
// First Read consumes only the first 5+len bytes.
type lazyConnectDevin struct {
	src io.ReadCloser
	s   *Server
	seq int64
	ctx context.Context

	buf []byte
	err error
}

func (l *lazyConnectDevin) Read(p []byte) (int, error) {
	return emitConnectFrame(p, &l.buf, &l.err, l.src, func(frame []byte) []byte {
		out, stats, catalog, processed := rewriteConnectDevinFrame(l.ctx, frame, l.s.Host, l.s.Client, l.s.Options)
		observeConnectFrame(l.s.events, l.seq, catalog)
		l.s.observeHostFrames(frame)
		if processed {
			l.record(frame, out, stats, catalog)
		}
		return out
	})
}

func (l *lazyConnectDevin) Close() error {
	return l.src.Close()
}

func (l *lazyConnectDevin) record(frame, out []byte, stats RewriteStats, catalog *CatalogShape) {
	l.s.events.Update(l.seq, func(e *Event) {
		if stats.Reason != "" {
			e.Reason = stats.Reason
		}
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
		e.Apply = stats.Apply
		e.Chosen = stats.Chosen
		e.Changed = stats.Changed
		e.ToolBefore = stats.ToolBefore
		e.ToolAfter = stats.ToolAfter
		e.CompactApplied = stats.CompactApplied
		e.ReasoningChanged = e.ReasoningChanged || stats.ReasoningChanged
		e.CompactDropped = stats.CompactDropped
		e.Catalog = mergeDevinCatalog(e.Catalog, catalog)
		e.Protocol = stats.Protocol
		copyDecisionRecord(e, stats)
	})
	l.s.applyConnectStats(stats, len(frame), len(out))
	if l.s.Log != nil && stats.Reason != "" && stats.Reason != reasonStream {
		l.s.Log.Print(FormatStats(stats))
	}
}

func rewriteConnectDevinFrame(ctx context.Context, frame []byte, h host.ID, client *jev.Client, opt Options) ([]byte, RewriteStats, *CatalogShape, bool) {
	var stats RewriteStats
	stats.Reason = reasonStream
	if len(frame) < 5 {
		return frame, stats, nil, false
	}
	flags := frame[0]
	payload := frame[5:]
	raw := payload
	if flags&connectFlagCompressed != 0 {
		gr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return frame, stats, protoFieldCatalog(payload), true
		}
		dec, err := io.ReadAll(gr)
		_ = gr.Close()
		if err != nil {
			return frame, stats, protoFieldCatalog(payload), true
		}
		raw = dec
	}

	found := collectProtoJSON(raw)
	if len(found) == 0 {
		return rewriteConnectDevinNativeProto(ctx, frame, flags, raw, h, client, opt)
	}
	chat, original := pickDevinChatJSON(found)
	if chat == nil {
		return rewriteConnectDevinNativeProto(ctx, frame, flags, raw, h, client, opt)
	}
	lifted, err := json.Marshal(chat)
	if err != nil {
		return frame, stats, protoFieldCatalog(raw), true
	}
	shape := catalogShape(lifted)
	rewritten, stats, err := RewriteWith(ctx, lifted, h, client, opt)
	if err != nil {
		return frame, stats, shape, true
	}
	if opt.AfterRewrite != nil {
		rewritten = opt.AfterRewrite(rewritten)
	}
	if !stats.Changed && string(rewritten) == original {
		return frame, stats, shape, true
	}
	newRaw, ok := rewriteProtoStrings(raw, func(s string) (string, bool) {
		if s == original {
			return string(rewritten), true
		}
		return "", false
	})
	if !ok || string(newRaw) == string(raw) {
		return frame, stats, shape, true
	}
	newPayload := newRaw
	if flags&connectFlagCompressed != 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(newRaw); err != nil {
			_ = zw.Close()
			return frame, stats, shape, true
		}
		if err := zw.Close(); err != nil {
			return frame, stats, shape, true
		}
		newPayload = buf.Bytes()
	}
	return connectFrame(flags, newPayload), stats, shape, true
}

func protoFieldCatalog(body []byte) *CatalogShape {
	fields, ok := parseProtoFields(body)
	counts := map[int]int{}
	protoJSON := 0
	history := map[string]int{"proto_json": 0}
	wire2 := map[int][]protoField{}
	if ok {
		for _, f := range fields {
			counts[f.field]++
			if f.wire == 2 {
				trimmed := bytes.TrimSpace(f.raw)
				if len(trimmed) > 0 && trimmed[0] == '{' {
					protoJSON++
				}
				wire2[f.field] = append(wire2[f.field], f)
			}
		}
	}
	history["proto_json"] = protoJSON
	keys := make([]string, 0, len(counts)+16)
	for n, c := range counts {
		if c > 1 {
			keys = append(keys, fmt.Sprintf("p%dx%d", n, c))
		} else {
			keys = append(keys, fmt.Sprintf("p%d", n))
		}
	}
	wireNums := make([]int, 0, len(wire2))
	for n, items := range wire2 {
		if protoCatalogInspectInner(n, len(items)) {
			wireNums = append(wireNums, n)
		}
	}
	sort.Ints(wireNums)
	for _, n := range wireNums {
		items := wire2[n]
		innerCounts := map[int]int{}
		textHits := map[int]int{}
		singleton := len(items) == 1 && (n == 1 || n == 4 || n == 8 || n == 29)
		for i, it := range items {
			raw := it.raw
			if n == 1 || n == 4 || n == 8 || n == 29 {
				if singleton {
					history[fmt.Sprintf("p%d_bytes", n)] = len(it.raw)
				}
				var gzipped bool
				raw, gzipped = unwrapProtoPayload(it.raw)
				if singleton && gzipped {
					history[fmt.Sprintf("p%d_gzip", n)] = 1
				}
			}
			inner, ok := parseProtoFields(raw)
			if singleton && ok {
				history[fmt.Sprintf("p%d_ok", n)] = 1
			}
			if !ok {
				continue
			}
			if i == 0 {
				for _, in := range inner {
					innerCounts[in.field]++
				}
			}
			for _, in := range inner {
				if in.wire == 2 && protoLikelyText(in.raw) {
					textHits[in.field]++
				}
			}
		}
		for in, c := range innerCounts {
			if c > 1 {
				keys = append(keys, fmt.Sprintf("p%di_p%dx%d", n, in, c))
			} else {
				keys = append(keys, fmt.Sprintf("p%di_p%d", n, in))
			}
		}
		for in, c := range textHits {
			history[fmt.Sprintf("p%d_text%d", n, in)] = c
		}
		if n == 2 {
			for _, it := range items {
				inner, ok := parseProtoFields(it.raw)
				if !ok {
					continue
				}
				for _, in := range inner {
					if in.field != 10 || in.wire != 2 {
						continue
					}
					history["p2_f10_bytes"] = len(in.raw)
					subRaw, gz := unwrapProtoPayload(in.raw)
					if gz {
						history["p2_f10_gzip"] = 1
					}
					sub, ok := parseProtoFields(subRaw)
					if !ok {
						continue
					}
					history["p2_f10_fields"] = len(sub)
					for _, s := range sub {
						keys = append(keys, fmt.Sprintf("p2i_p10i_p%d", s.field))
						if s.wire == 2 {
							history[fmt.Sprintf("p2_f10_p%d_bytes", s.field)] = len(s.raw)
						}
					}
					history["p2_f10_tools"] = len(bestToolBag(in.raw, nil, 0).tools)
				}
			}
		}
	}
	sort.Strings(keys)
	return &CatalogShape{
		Keys:         keys,
		HistoryTypes: history,
	}
}

func protoCatalogInspectInner(n, count int) bool {
	if count >= 2 {
		return true
	}
	if count < 1 {
		return false
	}
	switch n {
	case 1, 2, 3, 4, 8, 29:
		return true
	}
	return false
}

type protoJSON struct {
	raw string
	obj map[string]any
}

func collectProtoJSON(body []byte) []protoJSON {
	var out []protoJSON
	collectProtoJSONAt(body, 0, &out)
	return out
}

func collectProtoJSONAt(body []byte, depth int, out *[]protoJSON) {
	if depth > 8 || len(body) == 0 {
		return
	}
	if appendProtoJSON(body, out) {
		return
	}
	fields, ok := parseProtoFields(body)
	if !ok {
		return
	}
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		if appendProtoJSON(f.raw, out) {
			continue
		}
		collectProtoJSONAt(f.raw, depth+1, out)
	}
}

func appendProtoJSON(raw []byte, out *[]protoJSON) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 {
		return false
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) != nil || obj == nil {
			return false
		}
		*out = append(*out, protoJSON{raw: string(trimmed), obj: obj})
		return true
	case '[':
		var arr []map[string]any
		if json.Unmarshal(trimmed, &arr) != nil || len(arr) == 0 {
			return false
		}
		for _, obj := range arr {
			if obj == nil {
				continue
			}
			enc, err := json.Marshal(obj)
			if err != nil {
				continue
			}
			*out = append(*out, protoJSON{raw: string(enc), obj: obj})
		}
		return true
	}
	return false
}

func pickDevinChatJSON(found []protoJSON) (map[string]any, string) {
	for _, item := range found {
		if devinChatEligible(item.obj) {
			return item.obj, item.raw
		}
	}
	merged := map[string]any{}
	for _, item := range found {
		if !looksDevinChatShape(item.obj) {
			continue
		}
		for k, v := range item.obj {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil, ""
	}
	if devinChatEligible(merged) {
		return merged, ""
	}
	return merged, ""
}

func looksDevinChatShape(obj map[string]any) bool {
	if obj == nil {
		return false
	}
	if _, ok := obj["tools"]; ok {
		return true
	}
	if _, ok := obj["functions"]; ok {
		return true
	}
	if _, ok := obj["messages"]; ok {
		return true
	}
	if _, ok := obj["prompt"]; ok {
		return true
	}
	if _, ok := obj["input"]; ok {
		return true
	}
	return false
}

func devinChatEligible(obj map[string]any) bool {
	if obj == nil {
		return false
	}
	_, tools := obj["tools"]
	_, fns := obj["functions"]
	if !tools && !fns {
		return false
	}
	_, hist := obj["messages"]
	_, input := obj["input"]
	_, prompt := obj["prompt"]
	return hist || input || prompt
}

type devinHistItem struct {
	idx int
	raw []byte
	msg map[string]any
}

type devinNativeLift struct {
	root         map[string]any
	toolBytes    map[string][]byte
	catalogField int
	histField    int
	histItems    []devinHistItem
}

func rewriteConnectDevinNativeProto(ctx context.Context, frame []byte, flags byte, raw []byte, h host.ID, client *jev.Client, opt Options) ([]byte, RewriteStats, *CatalogShape, bool) {
	stats := RewriteStats{Reason: reasonStream}
	lift, ok := liftDevinNativeProto(raw)
	if !ok {
		return frame, stats, protoFieldCatalog(raw), true
	}
	catalogBody, err := json.Marshal(stripDevinLiftMeta(lift.root))
	if err != nil {
		return frame, stats, protoFieldCatalog(raw), true
	}
	lifted, err := json.Marshal(lift.root)
	if err != nil {
		return frame, stats, protoFieldCatalog(raw), true
	}
	shape := mergeDevinCatalog(catalogShape(catalogBody), protoFieldCatalog(raw))
	rewritten, stats, err := RewriteWith(ctx, lifted, h, client, opt)
	if err != nil {
		return frame, stats, shape, true
	}
	if opt.AfterRewrite != nil {
		rewritten = opt.AfterRewrite(rewritten)
	}
	if !stats.Changed && bytes.Equal(rewritten, lifted) {
		return frame, stats, shape, true
	}
	var next map[string]any
	if err := json.Unmarshal(rewritten, &next); err != nil {
		return frame, stats, shape, true
	}
	newRaw := replaceRepeatedBytesField(raw, lift.catalogField, keptToolRaws(devinLiftedToolNames(next), lift.toolBytes))
	if histRaw, ok := writebackDevinHistory(newRaw, lift, next); ok {
		newRaw = histRaw
	}
	if bytes.Equal(newRaw, raw) {
		return frame, stats, shape, true
	}
	newPayload := newRaw
	if flags&connectFlagCompressed != 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(newRaw); err != nil {
			_ = zw.Close()
			return frame, stats, shape, true
		}
		if err := zw.Close(); err != nil {
			return frame, stats, shape, true
		}
		newPayload = buf.Bytes()
	}
	return connectFrame(flags, newPayload), stats, shape, true
}

func liftDevinNativeProto(body []byte) (devinNativeLift, bool) {
	fields, ok := parseProtoFields(body)
	if !ok {
		return devinNativeLift{}, false
	}
	byField := map[int][]protoField{}
	order := make([]int, 0, 8)
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		if _, seen := byField[f.field]; !seen {
			order = append(order, f.field)
		}
		byField[f.field] = append(byField[f.field], f)
	}

	var histMsgs []any
	var histItems []devinHistItem
	histField := 0
	if msgs, items, ok := liftDevinHistoryField(byField[3]); ok {
		histMsgs, histItems, histField = msgs, items, 3
	}

	catalogField := 0
	var tools []any
	toolBytes := map[string][]byte{}
	best := 0
	for _, n := range order {
		if n == histField {
			continue
		}
		items := byField[n]
		if len(items) < 3 {
			continue
		}
		lifted, tb, ok := liftDevinCatalogItems(items)
		if !ok || len(lifted) <= best {
			continue
		}
		best = len(lifted)
		catalogField = n
		tools = lifted
		toolBytes = tb
	}
	if catalogField == 0 || len(tools) == 0 {
		return devinNativeLift{}, false
	}

	if histField == 0 {
		histMsgs, histField, histItems = liftDevinHistory(byField, order, catalogField)
	}

	root := map[string]any{"tools": tools}
	if len(histMsgs) > 0 {
		root["messages"] = histMsgs
	} else if prompt := pickDevinProtoPrompt(byField, catalogField); prompt != "" {
		root["prompt"] = prompt
	}
	return devinNativeLift{
		root: root, toolBytes: toolBytes, catalogField: catalogField,
		histField: histField, histItems: histItems,
	}, true
}

func liftDevinCatalogItems(items []protoField) ([]any, map[string][]byte, bool) {
	tools := make([]any, 0, len(items))
	tb := map[string][]byte{}
	for _, it := range items {
		name, desc := devinToolNameDesc(it.raw)
		if !isToolLikeName(name) {
			continue
		}
		tb[name] = append([]byte(nil), it.raw...)
		tool := map[string]any{"name": name}
		if desc != "" {
			tool["description"] = desc
		}
		tools = append(tools, tool)
	}
	return tools, tb, len(tools) >= 3
}

func liftDevinHistory(byField map[int][]protoField, order []int, catalogField int) ([]any, int, []devinHistItem) {
	if catalogField != 3 {
		if msgs, items, ok := liftDevinHistoryField(byField[3]); ok {
			return msgs, 3, items
		}
	}
	for _, n := range order {
		if n == catalogField || n == 3 {
			continue
		}
		if msgs, items, ok := liftDevinHistoryField(byField[n]); ok {
			return msgs, n, items
		}
	}
	return nil, 0, nil
}

func liftDevinChatMessage(item []byte) (map[string]any, bool) {
	fields, ok := parseProtoFields(item)
	if !ok {
		return nil, false
	}
	role := ""
	content := ""
	toolName := ""
	for _, f := range fields {
		if f.wire != 2 || !protoLikelyText(f.raw) {
			continue
		}
		s := string(f.raw)
		low := strings.ToLower(s)
		if low == "user" || low == "assistant" || low == "system" || low == "tool" {
			role = low
			continue
		}
		if isToolIdentName(s) {
			if toolName == "" || len(s) < len(toolName) {
				toolName = s
			}
			continue
		}
		if len(s) >= 1 && len(s) > len(content) {
			content = s
		}
	}
	if content == "" {
		return nil, false
	}
	if toolName != "" {
		switch role {
		case "tool", "function":
			return map[string]any{
				"role":         "tool",
				"name":         toolName,
				"tool_call_id": "",
				"content":      content,
			}, true
		case "assistant":
			return map[string]any{
				"role":    "assistant",
				"content": content,
				"tool_calls": []any{
					map[string]any{
						"id":   "",
						"type": "function",
						"function": map[string]any{
							"name":      toolName,
							"arguments": content,
						},
					},
				},
			}, true
		case "":
			return map[string]any{
				"role":    "",
				"content": content,
				"_tool":   toolName,
			}, true
		}
	}
	if role == "" && !orchestratorBrief(content) {
		role = "user"
	}
	return map[string]any{"role": role, "content": content}, true
}

func orchestratorBrief(content string) bool {
	low := strings.ToLower(content)
	return strings.Contains(low, "you are") ||
		strings.Contains(low, "codebase thoroughly") ||
		strings.Contains(low, "delegate")
}

func liftDevinHistoryField(items []protoField) ([]any, []devinHistItem, bool) {
	if len(items) < 2 {
		return nil, nil, false
	}
	msgs := make([]any, 0, len(items))
	kept := make([]devinHistItem, 0, len(items))
	for i, it := range items {
		msg, ok := liftDevinChatMessage(it.raw)
		if !ok {
			continue
		}
		msg["_idx"] = i
		msgs = append(msgs, msg)
		kept = append(kept, devinHistItem{idx: i, raw: append([]byte(nil), it.raw...), msg: msg})
	}
	if len(msgs) < 2 {
		return nil, nil, false
	}
	pairDevinToolTurns(msgs)
	return msgs, kept, true
}

func pairDevinToolTurns(msgs []any) {
	next := 0
	id := func() string {
		next++
		return "c" + strconv.Itoa(next)
	}
	lastID := ""
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if tool, _ := m["_tool"].(string); tool != "" {
			if lastID == "" {
				cid := id()
				m["role"] = "assistant"
				m["tool_calls"] = []any{
					map[string]any{
						"id":   cid,
						"type": "function",
						"function": map[string]any{
							"name":      tool,
							"arguments": m["content"],
						},
					},
				}
				lastID = cid
			} else {
				m["role"] = "tool"
				m["name"] = tool
				m["tool_call_id"] = lastID
				lastID = ""
			}
			delete(m, "_tool")
			continue
		}
		if tcs := asSlice(m["tool_calls"]); len(tcs) > 0 {
			tc, _ := tcs[0].(map[string]any)
			if tc != nil {
				if s, _ := tc["id"].(string); s == "" {
					tc["id"] = id()
				}
				lastID, _ = tc["id"].(string)
			}
			continue
		}
		if role, _ := m["role"].(string); role == "tool" {
			if s, _ := m["tool_call_id"].(string); s == "" {
				if lastID == "" {
					lastID = id()
				}
				m["tool_call_id"] = lastID
			}
			lastID = ""
		}
	}
}

func stripDevinLiftMeta(root map[string]any) map[string]any {
	out := cloneMap(root)
	msgs := asSlice(out["messages"])
	if len(msgs) == 0 {
		return out
	}
	cleaned := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			cleaned = append(cleaned, raw)
			continue
		}
		delete(m, "_idx")
		delete(m, "_tool")
		cleaned = append(cleaned, m)
	}
	out["messages"] = cleaned
	return out
}

func mergeDevinCatalog(shape, proto *CatalogShape) *CatalogShape {
	if shape == nil {
		return proto
	}
	if proto == nil {
		return shape
	}
	seen := map[string]bool{}
	for _, k := range shape.Keys {
		seen[k] = true
	}
	for _, k := range proto.Keys {
		if !seen[k] {
			shape.Keys = append(shape.Keys, k)
			seen[k] = true
		}
	}
	sort.Strings(shape.Keys)
	if shape.HistoryTypes == nil {
		shape.HistoryTypes = map[string]int{}
	}
	// Keep max for _bytes/_gzip/_ok and p*_text so a later empty
	// conversation_state cannot erase an earlier non-empty payload.
	// Max is also safer for other HistoryTypes counts.
	for k, v := range proto.HistoryTypes {
		if old := shape.HistoryTypes[k]; old > v {
			v = old
		}
		shape.HistoryTypes[k] = v
	}
	return shape
}

func writebackDevinHistory(raw []byte, lift devinNativeLift, next map[string]any) ([]byte, bool) {
	remaining := asSlice(next["messages"])
	if lift.histField == 0 || len(lift.histItems) == 0 {
		return raw, false
	}
	fields, ok := parseProtoFields(raw)
	if !ok {
		return raw, false
	}
	originals := make([][]byte, 0, len(fields))
	for _, f := range fields {
		if f.field == lift.histField && f.wire == 2 {
			originals = append(originals, f.raw)
		}
	}
	if len(originals) == 0 {
		return raw, false
	}

	used := make([]bool, len(lift.histItems))
	remainingByIdx := make(map[int]map[string]any, len(remaining))
	for _, remRaw := range remaining {
		rem, ok := remRaw.(map[string]any)
		if !ok {
			return raw, false
		}
		if idx, ok := devinMsgIdx(rem); ok {
			remainingByIdx[idx] = rem
			continue
		}
		item, i, ok := matchDevinHistRemaining(lift.histItems, used, rem)
		if !ok {
			return raw, false
		}
		used[i] = true
		remainingByIdx[item.idx] = rem
	}

	liftedByIdx := make(map[int]devinHistItem, len(lift.histItems))
	for _, item := range lift.histItems {
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
			if newText, trunc := devinHistTruncated(item, rem); trunc {
				itemRaw = rewriteDevinHistItemText(itemRaw, newText)
				changed = true
			}
			out = append(out, itemRaw)
		default:
			itemRaw := rewriteDevinHistItemText(orig, stub)
			if !bytes.Equal(itemRaw, orig) {
				changed = true
			}
			out = append(out, itemRaw)
		}
	}
	if !changed || len(out) != len(originals) || len(out) == 0 {
		return raw, false
	}
	return replaceRepeatedBytesField(raw, lift.histField, out), true
}

func matchDevinHistRemaining(items []devinHistItem, used []bool, rem map[string]any) (devinHistItem, int, bool) {
	if idx, ok := devinMsgIdx(rem); ok {
		for i, item := range items {
			if used[i] || item.idx != idx {
				continue
			}
			return item, i, true
		}
	}
	for i, item := range items {
		if used[i] || !devinHistMatch(item, rem) {
			continue
		}
		return item, i, true
	}
	return devinHistItem{}, -1, false
}

func devinMsgIdx(m map[string]any) (int, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m["_idx"].(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		idx := int(v)
		if float64(idx) == v {
			return idx, true
		}
	}
	return 0, false
}

func devinHistMatch(item devinHistItem, rem map[string]any) bool {
	if id := devinTurnID(item.msg); id != "" && id == devinTurnID(rem) {
		return true
	}
	ir, _ := item.msg["role"].(string)
	rr, _ := rem["role"].(string)
	if ir == "user" && rr == "user" && devinMessageText(item.msg) == devinMessageText(rem) {
		return true
	}
	return false
}

func devinTurnID(m map[string]any) string {
	if m == nil {
		return ""
	}
	if s, _ := m["tool_call_id"].(string); s != "" {
		return s
	}
	if tcs := asSlice(m["tool_calls"]); len(tcs) > 0 {
		if tc, _ := tcs[0].(map[string]any); tc != nil {
			if id, _ := tc["id"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

func devinMessageText(m map[string]any) string {
	if m == nil {
		return ""
	}
	if s, ok := m["content"].(string); ok && s != "" {
		return s
	}
	if tcs := asSlice(m["tool_calls"]); len(tcs) > 0 {
		if tc, _ := tcs[0].(map[string]any); tc != nil {
			if fn, _ := tc["function"].(map[string]any); fn != nil {
				if a, _ := fn["arguments"].(string); a != "" {
					return a
				}
			}
		}
	}
	return ""
}

func devinHistTruncated(item devinHistItem, rem map[string]any) (string, bool) {
	before := devinMessageText(item.msg)
	after := devinMessageText(rem)
	if after == "" || before == "" || after == before {
		return "", false
	}
	if strings.Contains(after, "jev-compaction truncated") || len(after) < len(before) {
		return after, true
	}
	return "", false
}

func rewriteDevinHistItemText(item []byte, newText string) []byte {
	fields, ok := parseProtoFields(item)
	if !ok {
		return item
	}
	bestField, bestLen := 0, -1
	for _, f := range fields {
		if f.wire != 2 || !protoLikelyText(f.raw) {
			continue
		}
		s := string(f.raw)
		low := strings.ToLower(s)
		if low == "user" || low == "assistant" || low == "system" || low == "tool" {
			continue
		}
		if isToolIdentName(s) {
			continue
		}
		if len(s) > bestLen {
			bestLen = len(s)
			bestField = f.field
		}
	}
	if bestField == 0 {
		out, _ := rewriteProtoStrings(item, func(s string) (string, bool) {
			if len(s) == bestLen && protoLikelyText([]byte(s)) {
				return newText, true
			}
			return "", false
		})
		return out
	}
	return replaceLengthField(item, bestField, []byte(newText))
}

func pickDevinProtoPrompt(byField map[int][]protoField, catalogField int) string {
	try := func(n int) string {
		if n == catalogField {
			return ""
		}
		items := byField[n]
		if len(items) != 1 || !protoLikelyText(items[0].raw) || len(items[0].raw) == 0 {
			return ""
		}
		return string(items[0].raw)
	}
	if s := try(1); s != "" {
		return s
	}
	if s := try(3); s != "" {
		return s
	}
	best := ""
	for n := range byField {
		if s := try(n); len(s) > len(best) {
			best = s
		}
	}
	return best
}

func devinToolNameDesc(def []byte) (name, desc string) {
	fields, ok := parseProtoFields(def)
	if !ok {
		return "", ""
	}
	var texts []string
	for _, f := range fields {
		if f.wire != 2 || !protoLikelyText(f.raw) {
			continue
		}
		s := string(f.raw)
		if f.field == 2 && desc == "" {
			desc = s
		}
		texts = append(texts, s)
	}
	best := ""
	for _, s := range texts {
		if !isToolIdentName(s) {
			continue
		}
		if best == "" || len(s) < len(best) {
			best = s
		}
	}
	if best != "" {
		return best, desc
	}
	for _, s := range texts {
		if !isToolLikeName(s) {
			continue
		}
		if best == "" || len(s) < len(best) {
			best = s
		}
	}
	return best, desc
}

func isToolIdentName(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	switch strings.ToLower(s) {
	case "user", "assistant", "system", "tool", "model":
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' || c == '/'):
		default:
			return false
		}
	}
	return true
}

func isToolLikeName(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	switch strings.ToLower(s) {
	case "user", "assistant", "system", "tool", "model":
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0 || (c < 0x20 && c != '\t') {
			return false
		}
	}
	return true
}

func devinLiftedToolNames(root map[string]any) []string {
	var names []string
	for _, d := range asSlice(root["tools"]) {
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
