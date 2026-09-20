package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func chatReq(user string, tools []any) []byte {
	raw, _ := json.Marshal(map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": user},
		},
		"tools": tools,
	})
	return raw
}

func workTools() []any {
	return []any{
		grokFn("grep", "search"),
		grokFn("read_file", "read"),
		grokFn("run_terminal_command", "shell"),
	}
}

func jevAnswers(t *testing.T, choice string, choiceConf, needs, needsConf float64, extra func(w http.ResponseWriter, r *http.Request)) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if extra != nil {
			extra(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": choice, "confidence": choiceConf},
				"needs_tool": map[string]any{"type": "noul", "noul": needs, "confidence": needsConf},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{APIKey: "k", BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

func TestGatewayDecision(t *testing.T) {
	unknown := "summarize this repo's architecture for me"
	tools := workTools()

	t.Run("adopt-boundary-0.85", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.85, 0.8, 0.8, nil)
		_, stats, err := Rewrite(chatReq(unknown, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Chosen != "grep" || stats.Source != sourceJev {
			t.Fatalf("%+v", stats)
		}
	})
	t.Run("just-below", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.849, 0.8, 0.8, nil)
		out, stats, err := Rewrite(chatReq(unknown, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Changed {
			t.Fatalf("low confidence must not rewrite: %+v %s", stats, out)
		}
	})
	t.Run("needs-mid-uncertain", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.5, 0.9, nil)
		_, stats, err := Rewrite(chatReq(unknown, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Changed {
			t.Fatalf("uncertain needs_tool must passthrough %+v", stats)
		}
	})
	t.Run("needs-no-tool", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.2, 0.9, nil)
		_, stats, err := Rewrite(chatReq(unknown, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Changed || stats.ToolAfter != stats.ToolBefore {
			t.Fatalf("no-tool-needed %+v", stats)
		}
	})
	t.Run("jev-error", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", 500)
		})
		raw := chatReq(unknown, tools)
		out, stats, err := Rewrite(raw, host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(raw) || stats.Changed {
			t.Fatalf("error must return original %+v", stats)
		}
	})
	t.Run("local-only", func(t *testing.T) {
		var calls int64
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			http.Error(w, "nope", 500)
		})
		_, stats, err := Rewrite(chatReq("Do not parallel. Sequential search and read the definition of RewriteWith.", tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Chosen != "read_file" || stats.Source != sourceLocal {
			t.Fatalf("%+v", stats)
		}
		if !strings.Contains(strings.Join(stats.ToolsAfter, ","), "read_file") {
			t.Fatalf("toolsAfter=%v; want read_file", stats.ToolsAfter)
		}
		if atomic.LoadInt64(&calls) != 0 {
			t.Fatal("selected sequential locate should skip Jev")
		}
	})
	t.Run("invalid-type", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"answers": map[string]any{"next_tool": "grep", "needs_tool": 1},
			})
		})
		raw := chatReq(unknown, tools)
		out, stats, _ := Rewrite(raw, host.Grok, c)
		if string(out) != string(raw) || stats.Reason != reasonInvalidJev {
			t.Fatalf("%+v", stats)
		}
	})
}

func TestHybridDefersHeuristics(t *testing.T) {
	tools := workTools()
	heuristic := "The auth middleware test is failing. Find it."
	selected := "Do not parallel. Sequential search and read the definition of RewriteWith."

	t.Run("word-match-asks-jev", func(t *testing.T) {
		var calls int64
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			}})
		})
		_, stats, err := Rewrite(chatReq(heuristic, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if atomic.LoadInt64(&calls) == 0 {
			t.Fatal("hybrid must ask Jev for word-match; fixed 0.86 skip is the old bug")
		}
		if stats.Source != sourceJev {
			t.Fatalf("%+v", stats)
		}
	})
	t.Run("selected-rule-skips-jev", func(t *testing.T) {
		var calls int64
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		})
		_, stats, err := Rewrite(chatReq(selected, tools), host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if atomic.LoadInt64(&calls) != 0 {
			t.Fatal("selected sequential locate must not ask Jev")
		}
		if stats.Source != sourceLocal || stats.Chosen != "read_file" {
			t.Fatalf("%+v", stats)
		}
	})
	t.Run("unconnected-keeps-catalog", func(t *testing.T) {
		raw := chatReq(heuristic, tools)
		out, stats, err := Rewrite(raw, host.Grok, nil)
		if err != nil {
			t.Fatal(err)
		}
		if stats.ToolAfter != stats.ToolBefore || stats.Apply != applyNone {
			t.Fatalf("hybrid defer without Jev must keep candidates %+v out=%s", stats, out)
		}
	})
	t.Run("invalid-jev-keeps-catalog", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"answers": map[string]any{"next_tool": "grep", "needs_tool": 1},
			})
		})
		raw := chatReq(heuristic, tools)
		out, stats, err := Rewrite(raw, host.Grok, c)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(raw) || stats.Reason != reasonInvalidJev {
			t.Fatalf("%+v", stats)
		}
	})
}

func TestSelectionModes(t *testing.T) {
	unknown := "summarize this repo's architecture for me"
	tools := workTools()
	t.Run("local never calls Jev", func(t *testing.T) {
		var calls int64
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		})
		o := DefaultOptions()
		o.SelectionMode = SelectionLocal
		_, stats, err := RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, o)
		if err != nil || stats.Source != sourceLocal || atomic.LoadInt64(&calls) != 0 {
			t.Fatalf("stats=%+v calls=%d err=%v", stats, calls, err)
		}
	})
	t.Run("jev delegates despite local confidence", func(t *testing.T) {
		var calls int64
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&calls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			}})
		})
		o := DefaultOptions()
		o.SelectionMode = SelectionJev
		_, stats, err := RewriteWith(nil, chatReq("The auth middleware test is failing. Find it.", tools), host.Grok, c, o)
		if err != nil || stats.Source != sourceJev || atomic.LoadInt64(&calls) != 1 {
			t.Fatalf("stats=%+v calls=%d err=%v", stats, calls, err)
		}
	})
	t.Run("jev uncertain passes full catalog", func(t *testing.T) {
		o := DefaultOptions()
		o.SelectionMode = SelectionJev
		raw := chatReq(unknown, tools)
		out, stats, err := RewriteWith(nil, raw, host.Grok, jevAnswers(t, "grep", 0.849, 0.9, 0.9, nil), o)
		if err != nil || stats.Changed || string(out) != string(raw) {
			t.Fatalf("stats=%+v err=%v", stats, err)
		}
	})
}

func TestAnalysisDecision(t *testing.T) {
	TestGatewayDecision(t)
}

func TestGatewayForced(t *testing.T) {
	unknown := "summarize this repo's architecture for me"
	tools := workTools()
	opt := DefaultOptions()
	opt.Mode = ModeForced

	t.Run("off-default-filter", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil)
		out, stats, err := RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		if hasRequiredToolChoice(got) {
			t.Fatalf("filter must not force %+v", got["tool_choice"])
		}
		if stats.Apply != applyFilter {
			t.Fatalf("apply=%s", stats.Apply)
		}
	})
	t.Run("fit-forced", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil)
		out, stats, err := RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, opt)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		if !hasRequiredToolChoice(got) {
			t.Fatalf("want forced choice %+v", got["tool_choice"])
		}
		if stats.Apply != applyForced || stats.ForcedTool != "grep" {
			t.Fatalf("%+v", stats)
		}
	})
	t.Run("local-stays-filter", func(t *testing.T) {
		out, stats, err := RewriteWith(nil, chatReq("Do not parallel. Sequential search and read the definition of RewriteWith.", tools), host.Grok, nil, opt)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		if hasRequiredToolChoice(got) {
			t.Fatal("local must not force")
		}
		if stats.Apply != applyFilter {
			t.Fatalf("apply=%s", stats.Apply)
		}
	})
	t.Run("explicit-choice", func(t *testing.T) {
		req := map[string]any{
			"model": "grok-4", "messages": []any{map[string]any{"role": "user", "content": unknown}},
			"tools": workTools(), "tool_choice": "required",
		}
		raw, _ := json.Marshal(req)
		out, stats, _ := RewriteWith(nil, raw, host.Grok, jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil), opt)
		if string(out) != string(raw) || stats.Changed {
			t.Fatalf("explicit choice %+v", stats)
		}
	})
	t.Run("model-unchanged", func(t *testing.T) {
		c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil)
		out, _, _ := RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, opt)
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		if got["model"] != "grok-4" {
			t.Fatalf("model=%v", got["model"])
		}
	})
}

func TestAnalysisForced(t *testing.T) {
	TestGatewayForced(t)
}

func TestGatewayArgsModel(t *testing.T) {
	opt := DefaultOptions()
	opt.Mode = ModeForced
	opt.ArgsModel = "cheap-arg"
	opt.ArgsTools = map[string]bool{"grep": true}
	unknown := "summarize this repo's architecture for me"
	c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil)
	out, stats, err := RewriteWith(nil, chatReq(unknown, workTools()), host.Grok, c, opt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["model"] != "cheap-arg" || stats.SentModel != "cheap-arg" || stats.OriginalModel != "grok-4" {
		t.Fatalf("model rewrite %+v body=%v", stats, got["model"])
	}
	c2 := jevAnswers(t, "read_file", 0.9, 0.9, 0.9, nil)
	out, stats, _ = RewriteWith(nil, chatReq(unknown, workTools()), host.Grok, c2, opt)
	_ = json.Unmarshal(out, &got)
	if got["model"] != "grok-4" {
		t.Fatalf("non-allowlisted changed model %v", got["model"])
	}
	_, err = OptionsFromEnv()
	// default env empty is ok
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisArgsModel(t *testing.T) {
	TestGatewayArgsModel(t)
}

func TestGatewayDirect(t *testing.T) {
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"mode": map[string]any{"type": "string", "const": "quick"},
		},
		"required": []any{"mode"},
	}
	tools := []any{map[string]any{"type": "function", "function": map[string]any{
		"name": "status", "parameters": schema,
	}}}
	opt := DefaultOptions()
	opt.Mode = ModeForced
	opt.DirectTools = map[string]bool{"status": true}
	unknown := "summarize this repo's architecture for me"
	c := jevAnswers(t, "status", 0.9, 0.9, 0.9, nil)
	_, stats, err := RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Direct || stats.DirectArgs != `{"mode":"quick"}` {
		t.Fatalf("direct stats %+v args=%s", stats, stats.DirectArgs)
	}

	allOf := map[string]any{
		"type": "object", "additionalProperties": false,
		"allOf":      []any{map[string]any{"required": []any{"path"}}},
		"properties": map[string]any{},
		"required":   []any{},
	}
	tools2 := []any{map[string]any{"type": "function", "function": map[string]any{"name": "status", "parameters": allOf}}}
	_, stats, _ = RewriteWith(nil, chatReq(unknown, tools2), host.Grok, c, opt)
	if stats.Direct {
		t.Fatal("allOf must not be direct")
	}

	off := DefaultOptions()
	off.Mode = ModeForced
	_, stats, _ = RewriteWith(nil, chatReq(unknown, tools), host.Grok, c, off)
	if stats.Direct {
		t.Fatal("empty allowlist must not be direct")
	}
}

func TestAnalysisDirect(t *testing.T) {
	TestGatewayDirect(t)
}

func TestGatewayDirectHTTP(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
		"required":             []any{},
	}
	body, _ := json.Marshal(map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "summarize this repo's architecture for me"},
		},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "ping", "parameters": schema,
		}}},
	})
	opt := DefaultOptions()
	opt.Mode = ModeForced
	opt.DirectTools = map[string]bool{"ping": true}
	c := jevAnswers(t, "ping", 0.9, 0.9, 0.9, nil)
	srv, err := NewWithOptions("127.0.0.1:0", host.Grok, c, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	var upstream int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&upstream, 1)
		w.WriteHeader(200)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	srv.Upstream = u
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if atomic.LoadInt64(&upstream) != 0 {
		t.Fatal("direct must not call upstream")
	}
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"finish_reason":"tool_calls"`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"usage"`) {
		t.Fatalf("direct response must not report provider usage: %s", rec.Body.String())
	}
	events, _, _, _ := srv.events.Snapshot(0)
	if len(events) != 1 || events[0].UsageMissing != "not_called" || events[0].SavedTokens == nil || events[0].SavedTokens.DirectInput == 0 {
		t.Fatalf("dashboard event missing direct savings: %+v", events)
	}
}

func TestGatewayDirectSSEUsage(t *testing.T) {
	opt := DefaultOptions()
	opt.Mode = ModeForced
	opt.DirectTools = map[string]bool{"status": true}
	srv, _ := testProxy(t, host.Grok, jevAnswers(t, "status", 0.9, 0.9, 0.9, nil), opt)
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}, "required": []any{}}
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "status", "parameters": schema}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatReq("find files", tools)))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), `"usage"`) {
		t.Fatalf("direct stream must not report provider usage: %s", rec.Body.String())
	}
	events, _, _, _ := srv.events.Snapshot(0)
	if len(events) != 1 || events[0].UsageMissing != "not_called" || events[0].SavedTokens == nil || events[0].SavedTokens.DirectInput == 0 {
		t.Fatalf("dashboard event missing direct savings: %+v", events)
	}
}
