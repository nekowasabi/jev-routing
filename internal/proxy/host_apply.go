package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const contextDelimiter = "\n\n--- jev-routing context ---\n"

// ApplyHostContext writes delivered application text back into the last user
// message. Cursor/Devin use the same JSON fields already rewritten by the
// existing Connect lifts; this does not invent tool_choice or hooks.
func ApplyHostContext(h host.ID, body []byte, extra string) ([]byte, error) {
	if strings.TrimSpace(extra) == "" {
		return body, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%s: body is not JSON: %w", h, err)
	}
	if !injectUserText(root, extra) {
		return nil, fmt.Errorf("%s: no user text field to write back", h)
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func injectUserText(root map[string]any, extra string) bool {
	if action, ok := root["action"].(map[string]any); ok && appendActionText(action, extra) {
		return true
	}
	if appendLastMessage(root, extra) {
		return true
	}
	if s, ok := root["text"].(string); ok && s != "" {
		root["text"] = s + contextDelimiter + extra
		return true
	}
	if s, ok := root["content"].(string); ok && s != "" {
		root["content"] = s + contextDelimiter + extra
		return true
	}
	if parts, ok := root["content"].([]any); ok {
		root["content"] = append(parts, map[string]any{"type": "text", "text": extra})
		return true
	}
	for _, key := range []string{"messages", "input", "turns", "conversation", "prompt", "text"} {
		if v, ok := root[key]; ok {
			if m, ok := v.(map[string]any); ok && injectUserText(m, extra) {
				return true
			}
			if arr, ok := v.([]any); ok {
				for i := len(arr) - 1; i >= 0; i-- {
					if m, ok := arr[i].(map[string]any); ok && injectUserText(m, extra) {
						return true
					}
				}
			}
			if s, ok := v.(string); ok && s != "" && (key == "prompt" || key == "text") {
				root[key] = s + contextDelimiter + extra
				return true
			}
		}
	}
	return false
}

func appendLastMessage(root map[string]any, extra string) bool {
	raw, ok := root["messages"]
	if !ok {
		raw, ok = root["input"]
	}
	if !ok {
		return false
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return false
	}
	last, ok := arr[len(arr)-1].(map[string]any)
	if !ok {
		return false
	}
	if s, ok := last["content"].(string); ok && s != "" {
		last["content"] = s + contextDelimiter + extra
		arr[len(arr)-1] = last
		if _, has := root["messages"]; has {
			root["messages"] = arr
		} else {
			root["input"] = arr
		}
		return true
	}
	if parts, ok := last["content"].([]any); ok {
		parts = append(parts, map[string]any{"type": "text", "text": extra})
		last["content"] = parts
		arr[len(arr)-1] = last
		if _, has := root["messages"]; has {
			root["messages"] = arr
		} else {
			root["input"] = arr
		}
		return true
	}
	return false
}

func appendActionText(action map[string]any, extra string) bool {
	for _, k := range []string{"userMessage", "user_message", "message", "prompt", "text", "content", "userMessageAction"} {
		if s, ok := action[k].(string); ok && s != "" {
			action[k] = s + contextDelimiter + extra
			return true
		}
		if m, ok := action[k].(map[string]any); ok {
			if injectUserText(m, extra) || appendActionText(m, extra) {
				return true
			}
		}
	}
	return false
}

func RequireApplied(policy string, app *Application, err error) error {
	if policy != PolicyRequired {
		return nil
	}
	if err != nil {
		return fmt.Errorf("required application failed: %w", err)
	}
	if app == nil || !Success(app) && app.State != AppStarted && app.State != AppResultReceived && app.State != AppVerified && app.State != AppDelivered {
		state := "missing"
		if app != nil {
			state = app.State
		}
		return fmt.Errorf("required application not consumed: %s", state)
	}
	return nil
}
