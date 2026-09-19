package proxy

import (
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// protoURLHosts returns unique hostnames from protobuf length-delimited
// strings that parse as http(s) URLs. Paths, queries, and credentials are dropped.
func mergeHosts(dst, src []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range append(append([]string{}, dst...), src...) {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func cursorAgentHost(host string) bool {
	h := strings.ToLower(host)
	return strings.Contains(h, "api5.cursor.sh") ||
		strings.HasPrefix(h, "agentn.") && strings.HasSuffix(h, ".cursor.sh")
}

func rewriteCursorAgentHosts(body []byte, listen string) []byte {
	listen = strings.TrimSpace(listen)
	if listen == "" || !bytesContainCursorAgentHost(body) {
		return body
	}
	out, ok := rewriteProtoStrings(body, func(s string) (string, bool) {
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			u, err := url.Parse(s)
			if err != nil || !cursorAgentHost(u.Host) {
				return s, false
			}
			u.Scheme = "http"
			u.Host = listen
			return u.String(), true
		}
		if cursorAgentHost(s) {
			return listen, true
		}
		return s, false
	})
	if !ok {
		return body
	}
	return out
}

func bytesContainCursorAgentHost(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "api5.cursor.sh") || strings.Contains(s, "agentn.")
}

func rewriteProtoStrings(body []byte, fn func(string) (string, bool)) ([]byte, bool) {
	out, changed, ok := rewriteProtoStringsAt(body, fn, 0)
	if !ok {
		return body, false
	}
	return out, changed
}

func rewriteProtoStringsAt(body []byte, fn func(string) (string, bool), depth int) ([]byte, bool, bool) {
	if depth > 6 {
		return body, false, true
	}
	var out []byte
	changed := false
	i := 0
	for i < len(body) {
		start := i
		tag, n := protoVarint(body[i:])
		if n <= 0 {
			return body, false, false
		}
		i += n
		switch tag & 7 {
		case 0:
			_, n = protoVarint(body[i:])
			if n <= 0 {
				return body, false, false
			}
			i += n
			out = append(out, body[start:i]...)
		case 1:
			if i+8 > len(body) {
				return body, false, false
			}
			i += 8
			out = append(out, body[start:i]...)
		case 2:
			ln, n := protoVarint(body[i:])
			if n <= 0 || ln < 0 || i+n+int(ln) > len(body) {
				return body, false, false
			}
			i += n
			raw := body[i : i+int(ln)]
			i += int(ln)
			if protoLikelyString(raw) || protoLikelyJSON(raw) {
				if next, ok := fn(string(raw)); ok && next != string(raw) {
					out = append(out, protoTagAndBytes(tag, []byte(next))...)
					changed = true
					continue
				}
			}
			nested, nestChanged, nestOK := rewriteProtoStringsAt(raw, fn, depth+1)
			if nestOK && nestChanged {
				out = append(out, protoTagAndBytes(tag, nested)...)
				changed = true
				continue
			}
			out = append(out, body[start:i]...)
		case 5:
			if i+4 > len(body) {
				return body, false, false
			}
			i += 4
			out = append(out, body[start:i]...)
		default:
			return body, false, false
		}
	}
	return out, changed, true
}

func protoTagAndBytes(tag uint64, raw []byte) []byte {
	out := append(protoEncodeVarint(tag), protoEncodeVarint(uint64(len(raw)))...)
	return append(out, raw...)
}

func protoEncodeVarint(x uint64) []byte {
	var b []byte
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

func protoURLHosts(body []byte) []string {
	seen := map[string]bool{}
	walkProtoStrings(body, 0, func(s string) {
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			return
		}
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return
		}
		seen[clipEvent(u.Host)] = true
	})
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func walkProtoStrings(body []byte, depth int, fn func(string)) {
	if depth > 6 || len(body) == 0 {
		return
	}
	i := 0
	for i < len(body) {
		tag, n := protoVarint(body[i:])
		if n <= 0 {
			return
		}
		i += n
		switch tag & 7 {
		case 0:
			_, n = protoVarint(body[i:])
			if n <= 0 {
				return
			}
			i += n
		case 1:
			if i+8 > len(body) {
				return
			}
			i += 8
		case 2:
			ln, n := protoVarint(body[i:])
			if n <= 0 || ln < 0 || i+n+int(ln) > len(body) {
				return
			}
			i += n
			raw := body[i : i+int(ln)]
			i += int(ln)
			if protoLikelyString(raw) {
				fn(string(raw))
			}
			walkProtoStrings(raw, depth+1, fn)
		case 5:
			if i+4 > len(body) {
				return
			}
			i += 4
		default:
			return
		}
	}
}

func protoVarint(b []byte) (uint64, int) {
	var x uint64
	for i := 0; i < len(b) && i < 10; i++ {
		x |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return x, i + 1
		}
	}
	return 0, 0
}

type protoHostCloser struct {
	rc     io.ReadCloser
	buf    []byte
	limit  int
	onDone func([]string)
	once   sync.Once
}

func wrapProtoHosts(rc io.ReadCloser, onDone func([]string)) io.ReadCloser {
	if rc == nil {
		return rc
	}
	return &protoHostCloser{rc: rc, limit: 1 << 16, onDone: onDone}
}

func (w *protoHostCloser) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if n > 0 && len(w.buf) < w.limit {
		take := n
		if len(w.buf)+take > w.limit {
			take = w.limit - len(w.buf)
		}
		w.buf = append(w.buf, p[:take]...)
	}
	if err == io.EOF {
		w.finish()
	}
	return n, err
}

func (w *protoHostCloser) Close() error {
	w.finish()
	return w.rc.Close()
}

func (w *protoHostCloser) finish() {
	w.once.Do(func() {
		if w.onDone != nil {
			w.onDone(protoURLHosts(w.buf))
		}
	})
}

func protoLikelyString(b []byte) bool {
	if len(b) == 0 || len(b) > 2048 {
		return false
	}
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

func protoLikelyJSON(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	switch b[0] {
	case '{', '[':
		return json.Valid(b)
	}
	return false
}
