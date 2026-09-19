package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type directCall struct {
	ID   string
	Name string
	Args string
}

func directArgsFor(tool any) (string, bool) {
	m, ok := tool.(map[string]any)
	if !ok {
		return "", false
	}
	schema := toolSchema(m)
	if schema == nil {
		return "", false
	}
	args, ok := constantObjectArgs(schema)
	if !ok {
		return "", false
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func toolSchema(m map[string]any) map[string]any {
	if fn, ok := m["function"].(map[string]any); ok {
		if p, ok := fn["parameters"].(map[string]any); ok {
			return p
		}
	}
	if p, ok := m["parameters"].(map[string]any); ok {
		return p
	}
	if p, ok := m["input_schema"].(map[string]any); ok {
		return p
	}
	return nil
}

func constantObjectArgs(schema map[string]any) (map[string]any, bool) {
	if !schemaVocabOK(schema, true) {
		return nil, false
	}
	typ, _ := schema["type"].(string)
	if typ != "object" {
		return nil, false
	}
	ap, ok := schema["additionalProperties"]
	if !ok {
		return nil, false
	}
	if b, ok := ap.(bool); !ok || b {
		return nil, false
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	reqRaw, ok := schema["required"].([]any)
	if !ok {
		return nil, false
	}
	required := map[string]bool{}
	for _, r := range reqRaw {
		s, ok := r.(string)
		if !ok {
			return nil, false
		}
		required[s] = true
	}
	if len(required) != len(props) {
		return nil, false
	}
	out := map[string]any{}
	for name, raw := range props {
		if !required[name] {
			return nil, false
		}
		ps, ok := raw.(map[string]any)
		if !ok || !schemaVocabOK(ps, false) {
			return nil, false
		}
		val, ok := scalarConst(ps)
		if !ok {
			return nil, false
		}
		out[name] = val
	}
	return out, true
}

func schemaVocabOK(m map[string]any, root bool) bool {
	allowed := map[string]bool{
		"type": true, "properties": true, "required": true, "additionalProperties": true,
		"title": true, "description": true, "const": true,
	}
	if !root {
		allowed["const"] = true
	}
	for k := range m {
		if !allowed[k] {
			return false
		}
	}
	if _, ok := m["$ref"]; ok {
		return false
	}
	for _, k := range []string{"allOf", "oneOf", "anyOf", "enum"} {
		if _, ok := m[k]; ok {
			return false
		}
	}
	return true
}

func scalarConst(ps map[string]any) (any, bool) {
	c, ok := ps["const"]
	if !ok {
		return nil, false
	}
	typ, _ := ps["type"].(string)
	switch typ {
	case "string":
		s, ok := c.(string)
		return s, ok
	case "boolean":
		b, ok := c.(bool)
		return b, ok
	case "integer":
		switch n := c.(type) {
		case float64:
			if n == math.Trunc(n) {
				return int(n), true
			}
		case json.Number:
			i, err := n.Int64()
			return int(i), err == nil
		case int:
			return n, true
		}
		return nil, false
	case "number":
		switch n := c.(type) {
		case float64:
			return n, true
		case json.Number:
			f, err := n.Float64()
			return f, err == nil
		case int:
			return float64(n), true
		}
		return nil, false
	default:
		return nil, false
	}
}

func newToolCallID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "call_" + strconv.FormatInt(int64(len(b)), 10)
	}
	return "call_" + hex.EncodeToString(b[:])
}

func directChatJSON(id, name, args, model string) []byte {
	body := map[string]any{
		"id":     "chatcmpl-direct-" + strings.TrimPrefix(id, "call_"),
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{map[string]any{
					"id":       id,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func directChatSSE(id, name, args, model string) []byte {
	chunk := map[string]any{
		"id":     "chatcmpl-direct-" + strings.TrimPrefix(id, "call_"),
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"index":    0,
					"id":       id,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
	raw, _ := json.Marshal(chunk)
	return []byte(fmt.Sprintf("data: %s\n\ndata: [DONE]\n\n", raw))
}
