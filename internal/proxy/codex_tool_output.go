package proxy

import (
	"fmt"
	"unicode/utf8"
)

// codexToolOutputMaxBytes is the fixed threshold applyCodexToolOutputMax
// truncates Codex tool outputs at. It is not configurable: only whether
// truncation runs at all is (JEV_CODEX_TOOL_OUTPUT_TRUNCATE; see options.go).
const codexToolOutputMaxBytes = 20000

// applyCodexToolOutputMax truncates the output of function_call_output,
// custom_tool_call_output, local_shell_call_output, and shell_call_output
// items in root["input"] whose text exceeds max bytes. It mutates root in
// place and reports how many outputs were truncated and how many bytes were
// cut. Every other item, tool definition, and field is left untouched.
//
// Codex resends its full history on every request, so this must run the same
// way on every request that carries a given tool output: same bytes in, same
// bytes out, independent of surrounding items or request size. Otherwise the
// upstream prompt-cache prefix breaks on every turn.
func applyCodexToolOutputMax(root map[string]any, max int) (truncated, bytesCut int) {
	if max <= 0 {
		return 0, 0
	}
	input, ok := root["input"].([]any)
	if !ok {
		return 0, 0
	}
	for _, it := range input {
		m, ok := it.(map[string]any)
		if !ok || !codexTruncatableOutput(str(m["type"])) {
			continue
		}
		switch out := m["output"].(type) {
		case string:
			if s, n, did := truncateMiddle(out, max); did {
				m["output"] = s
				truncated++
				bytesCut += n
			}
		case []any:
			for _, part := range out {
				pm, ok := part.(map[string]any)
				if !ok {
					continue
				}
				text, ok := pm["text"].(string)
				if !ok {
					continue
				}
				if s, n, did := truncateMiddle(text, max); did {
					pm["text"] = s
					truncated++
					bytesCut += n
				}
			}
		}
	}
	return truncated, bytesCut
}

func codexTruncatableOutput(typ string) bool {
	switch typ {
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output":
		return true
	default:
		return false
	}
}

// truncateMiddle keeps the first and last max/2 bytes of s (adjusted to the
// nearest UTF-8 rune boundary) and replaces the middle with a note. It is a
// pure function of s and max, so the same input always yields the same bytes.
func truncateMiddle(s string, max int) (out string, omitted int, did bool) {
	if len(s) <= max {
		return s, 0, false
	}
	half := max / 2
	headLen := utf8HeadLen(s, half)
	tailStart := utf8TailStart(s, len(s)-half)
	if tailStart < headLen {
		tailStart = headLen
	}
	omitted = tailStart - headLen
	note := fmt.Sprintf("\n[jev-routing: omitted %d of %d bytes from the middle of this tool output; re-run a narrower command (e.g. sed -n 'A,Bp' or rg) to see the omitted part]\n", omitted, len(s))
	return s[:headLen] + note + s[tailStart:], omitted, true
}

// utf8HeadLen returns the largest length <= n that keeps s[:length] valid UTF-8.
func utf8HeadLen(s string, n int) int {
	if n > len(s) {
		n = len(s)
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return n
}

// utf8TailStart returns the smallest offset >= start that keeps s[offset:] valid UTF-8.
func utf8TailStart(s string, start int) int {
	if start < 0 {
		start = 0
	}
	if start > len(s) {
		start = len(s)
	}
	for start < len(s) && !utf8.ValidString(s[start:]) {
		start++
	}
	return start
}
