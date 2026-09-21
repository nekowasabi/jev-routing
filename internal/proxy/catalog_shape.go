package proxy

import (
	"encoding/json"
	"sort"
	"strings"
)

// CatalogShape records schema metadata only: never prompts, arguments or headers.
type CatalogShape struct {
	Keys                       []string       `json:"keys"`
	ToolsType                  string         `json:"toolsType"`
	Location                   string         `json:"location"`
	RawCount                   int            `json:"rawCount"`
	FlatCount                  int            `json:"flatCount"`
	Candidates                 int            `json:"candidates"`
	Definitions                []ToolShape    `json:"definitions,omitempty"`
	HistoryTypes               map[string]int `json:"historyTypes,omitempty"`
	ContentTypes               map[string]int `json:"contentTypes,omitempty"`
	DeferredCount              int            `json:"deferredCount"`
	ToolSearchPresent          bool           `json:"toolSearchPresent"`
	DeferredPlaceholderPresent bool           `json:"deferredPlaceholderPresent"`
	SystemReminders            int            `json:"systemReminders"`
}

type ToolShape struct {
	Type     string      `json:"type"`
	Keys     []string    `json:"keys,omitempty"`
	Children []ToolShape `json:"children,omitempty"`
}

func shapeKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, clipEvent(k))
	}
	sort.Strings(keys)
	return keys
}

func shapeType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}

func toolShapes(items []any, depth int) []ToolShape {
	var out []ToolShape
	for _, raw := range items {
		s := ToolShape{Type: shapeType(raw)}
		if m, ok := raw.(map[string]any); ok {
			s.Keys = shapeKeys(m)
			if typ, ok := m["type"].(string); ok {
				s.Type = clipEvent(typ)
			}
			if depth > 0 {
				for _, key := range []string{"tools", "functions"} {
					s.Children = append(s.Children, toolShapes(asSlice(m[key]), depth-1)...)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

func historyShapeType(value any) string {
	if m, ok := value.(map[string]any); ok {
		if typ, ok := m["type"].(string); ok {
			return clipEvent(typ)
		}
	}
	return shapeType(value)
}

func countContentTypes(value any, counts map[string]int) {
	items, ok := value.([]any)
	if !ok {
		counts[shapeType(value)]++
		return
	}
	for _, item := range items {
		counts[historyShapeType(item)]++
		if block, ok := item.(map[string]any); ok {
			if content, exists := block["content"]; exists {
				countContentTypes(content, counts)
			}
		}
	}
}

func catalogShape(body []byte) *CatalogShape {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil
	}
	raw := extractRawTools(root)
	flat, location := extractTools(root)
	historyTypes, contentTypes := map[string]int{}, map[string]int{}
	shape := &CatalogShape{}
	for _, raw := range flat {
		if tool, ok := raw.(map[string]any); ok {
			if deferred, _ := tool["defer_loading"].(bool); deferred {
				shape.DeferredCount++
			}
			if toolNameOf(tool) == "ToolSearch" {
				shape.ToolSearchPresent = true
			}
			if toolNameOf(tool) == "DeferredToolPlaceholder" {
				shape.DeferredPlaceholderPresent = true
			}
		}
	}
	for _, slot := range historySlots(root) {
		for _, item := range slot.msgs {
			historyTypes[slot.prefix+"."+historyShapeType(item)]++
			if message, ok := item.(map[string]any); ok {
				shape.SystemReminders += strings.Count(textOf(message), "<system-reminder>")
				if content, exists := message["content"]; exists {
					countContentTypes(content, contentTypes)
				}
			}
		}
	}
	keys := shapeKeys(root)
	return &CatalogShape{Keys: keys, ToolsType: shapeType(root["tools"]), Location: location,
		RawCount: len(raw), FlatCount: len(flat), Candidates: len(filterableTools(flat)),
		Definitions: toolShapes(raw, 2), HistoryTypes: historyTypes, ContentTypes: contentTypes,
		DeferredCount: shape.DeferredCount, ToolSearchPresent: shape.ToolSearchPresent,
		DeferredPlaceholderPresent: shape.DeferredPlaceholderPresent, SystemReminders: shape.SystemReminders}
}
