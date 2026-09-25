package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

// codexSteerJev stands up a fixed-response Jev server answering the
// codex-steer "tool"/"needs_tool" questions, matching this repo's existing
// jevAnswers helper style (see gateway_decision_test.go).
func codexSteerJev(t *testing.T, choice string, choiceConf, needs, needsConf float64) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				codexToolKey:      map[string]any{"type": "choice", "choice": choice, "confidence": choiceConf, "probabilities": map[string]float64{choice: 1}},
				codexNeedsToolKey: map[string]any{"type": "noul", "noul": needs, "confidence": needsConf},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{APIKey: "k", BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

// codexSteerJevMustNotBeCalled fails the test if Jev is ever asked.
func codexSteerJevMustNotBeCalled(t *testing.T) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Jev must not be called for this request")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{APIKey: "k", BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

func codexSteerOpt() Options {
	o := DefaultOptions()
	o.CodexSteer = true
	return o
}

func codexReadFileReq(extra map[string]any) map[string]any {
	req := map[string]any{
		"model": "gpt-5.6-terra",
		"tools": []any{
			map[string]any{"type": "function", "name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object"}},
		},
		"input": []any{
			map[string]any{"role": "user", "content": "please read foo.txt"},
		},
		"parallel_tool_calls": true,
		"reasoning":           map[string]any{"effort": "medium"},
	}
	for k, v := range extra {
		req[k] = v
	}
	return req
}

func mustMarshalReq(t *testing.T, req map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCodexSteerForcedFunctionToolChoice(t *testing.T) {
	client := codexSteerJev(t, "read_file", 0.9, 0.9, 0.9)
	req := codexReadFileReq(nil)
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apply != applyForced || stats.ForcedTool != "read_file" || stats.Chosen != "read_file" {
		t.Fatalf("want forced read_file, got %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tc, ok := got["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "function" || tc["name"] != "read_file" {
		t.Fatalf("bad tool_choice: %v", got["tool_choice"])
	}
	delete(got, "tool_choice")
	delete(req, "tool_choice")
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("only tool_choice should change:\nwant %+v\ngot  %+v", req, got)
	}
}

func TestCodexSteerForcedCustomToolChoice(t *testing.T) {
	client := codexSteerJev(t, "run_shell", 0.9, 0.9, 0.9)
	req := codexReadFileReq(map[string]any{
		"tools": []any{
			map[string]any{"type": "custom", "name": "run_shell", "description": "Run a free-form shell command"},
		},
	})
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apply != applyForced || stats.ForcedTool != "run_shell" {
		t.Fatalf("want forced run_shell, got %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tc, ok := got["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "custom" || tc["name"] != "run_shell" {
		t.Fatalf("bad tool_choice: %v", got["tool_choice"])
	}
}

func TestCodexSteerNoneToolChoice(t *testing.T) {
	client := codexSteerJev(t, codexNoTool, 0.9, 0.1, 0.9)
	req := codexReadFileReq(nil)
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apply != applyCodexNone {
		t.Fatalf("want applyCodexNone, got %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["tool_choice"] != "none" {
		t.Fatalf("want tool_choice none, got %v", got["tool_choice"])
	}
	delete(got, "tool_choice")
	delete(req, "tool_choice")
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("only tool_choice should change:\nwant %+v\ngot  %+v", req, got)
	}
}

func TestCodexSteerLowConfidencePassthrough(t *testing.T) {
	client := codexSteerJev(t, "read_file", 0.5, 0.9, 0.9)
	raw := mustMarshalReq(t, codexReadFileReq(nil))
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCodexLowConfidence {
		t.Fatalf("want low_confidence passthrough, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerAnswersDisagreePassthrough(t *testing.T) {
	// Tool chosen, but necessity < 0.3.
	client := codexSteerJev(t, "read_file", 0.9, 0.1, 0.9)
	raw := mustMarshalReq(t, codexReadFileReq(nil))
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCodexAnswersDisagree {
		t.Fatalf("want jev_answers_disagree, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerNamespacedSelectionPassthrough(t *testing.T) {
	client := codexSteerJev(t, "browser.click", 0.9, 0.9, 0.9)
	req := codexReadFileReq(map[string]any{
		"tools": []any{
			map[string]any{
				"type": "namespace",
				"name": "browser",
				"tools": []any{
					map[string]any{"type": "function", "name": "click", "description": "Click an element"},
				},
			},
		},
	})
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCodexNamespaced {
		t.Fatalf("want namespaced_tool_selected, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerAgentMessagePassthrough(t *testing.T) {
	client := codexSteerJevMustNotBeCalled(t)
	req := codexReadFileReq(map[string]any{
		"input": []any{
			map[string]any{"role": "user", "content": "please read foo.txt"},
			map[string]any{"type": "agent_message", "content": "sub-agent said something"},
		},
	})
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCodexAgentMessage {
		t.Fatalf("want agent_message passthrough, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerPreviousResponseIDPassthrough(t *testing.T) {
	client := codexSteerJevMustNotBeCalled(t)
	req := codexReadFileReq(map[string]any{"previous_response_id": "resp_abc123"})
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonPreviousResponse {
		t.Fatalf("want previous_response_id passthrough, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerExplicitToolChoicePassthrough(t *testing.T) {
	client := codexSteerJevMustNotBeCalled(t)
	req := codexReadFileReq(map[string]any{"tool_choice": map[string]any{"type": "function", "name": "read_file"}})
	raw := mustMarshalReq(t, req)
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason != reasonCodexToolChoiceSet {
		t.Fatalf("want tool_choice_already_decided passthrough, got %+v", stats)
	}
	if string(out) != string(raw) {
		t.Fatalf("passthrough must not modify body")
	}
}

func TestCodexSteerFlagOffLeavesCodexUnchanged(t *testing.T) {
	client := codexSteerJevMustNotBeCalled(t)
	raw := mustMarshalReq(t, codexReadFileReq(nil))
	opt := DefaultOptions()
	opt.Mode = ModeBaseline // CodexSteer off (default), take the ordinary baseline path
	out, stats, err := RewriteWith(context.Background(), raw, host.Codex, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Fatalf("expected untouched baseline passthrough")
	}
	if stats.Apply == applyForced || stats.Apply == applyCodexNone {
		t.Fatalf("codex-steer must not run when the flag is off: %+v", stats)
	}
}

func TestCodexSteerNonCodexHostUnaffected(t *testing.T) {
	client := codexSteerJevMustNotBeCalled(t)
	raw := mustMarshalReq(t, map[string]any{
		"model": "claude-x",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	})
	opt := codexSteerOpt() // CodexSteer on, but host is Claude
	opt.Mode = ModeBaseline
	out, stats, err := RewriteWith(context.Background(), raw, host.Claude, client, opt)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Fatalf("non-Codex host must be unaffected by JEV_CODEX_STEER")
	}
	if stats.Apply == applyForced || stats.Apply == applyCodexNone {
		t.Fatalf("codex-steer must never run for a non-Codex host: %+v", stats)
	}
}

func TestCodexSteerEnvParsing(t *testing.T) {
	t.Setenv("JEV_CODEX_STEER", "")
	o, err := OptionsFromEnv()
	if err != nil || o.CodexSteer {
		t.Fatalf("unset should default to off: o=%+v err=%v", o, err)
	}
	t.Setenv("JEV_CODEX_STEER", "on")
	o, err = OptionsFromEnv()
	if err != nil || !o.CodexSteer {
		t.Fatalf("on should enable: o=%+v err=%v", o, err)
	}
	t.Setenv("JEV_CODEX_STEER", "off")
	o, err = OptionsFromEnv()
	if err != nil || o.CodexSteer {
		t.Fatalf("off should disable: o=%+v err=%v", o, err)
	}
	t.Setenv("JEV_CODEX_STEER", "maybe")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("expected error for an invalid value")
	}
}

// TestCodexSteerUpstreamRejectionResendsOriginal mirrors jev-gateway
// app.js:138-152: a rewritten request upstream rejects with 400 is resent
// exactly once with the untouched original body.
func TestCodexSteerUpstreamRejectionResendsOriginal(t *testing.T) {
	client := codexSteerJev(t, "read_file", 0.9, 0.9, 0.9)
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		n := atomic.AddInt32(&calls, 1)
		_, hasChoice := decoded["tool_choice"]
		if n == 1 {
			if !hasChoice {
				t.Errorf("first upstream call should carry the rewritten tool_choice")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad tool_choice"}}`))
			return
		}
		if hasChoice {
			t.Errorf("resend must use the ORIGINAL body (no tool_choice)")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","output":[]}`))
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("CODEX_UPSTREAM", upstream.URL)

	srv, err := NewWithOptions("127.0.0.1:0", host.Codex, client, io.Discard, codexSteerOpt())
	if err != nil {
		t.Fatal(err)
	}
	raw := mustMarshalReq(t, codexReadFileReq(nil))
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 after resend, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("want exactly 2 upstream calls (original + resend), got %d", got)
	}
}
