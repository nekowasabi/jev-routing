package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
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

func connectContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "proto")
}

func (s *Server) applyConnectStats(stats RewriteStats, before, after int) {
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
