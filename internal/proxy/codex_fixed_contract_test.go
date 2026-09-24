package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestCodexFixedWireContract(t *testing.T) {
	var forwarded []byte
	var control []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/responses":
			forwarded = body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":36,\"output_tokens\":8,\"input_tokens_details\":{\"cached_tokens\":22},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n")
		case "/v1/telemetry":
			control = body
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	opt := localOpt()
	opt.Reasoning = ReasoningPreserve
	s, err := NewWithOptions("127.0.0.1:0", host.Codex, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	s.Upstream, _ = url.Parse(upstream.URL)
	request := `{"model":"gpt-5.6-terra","reasoning":{"effort":"medium"},"tools":[{"type":"web_search"}],"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"grep_files"},{"type":"function","name":"read_file"}]}]},{"role":"user","content":"Search the repo for the definition of authMiddleware"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(forwarded) == 0 {
		t.Fatalf("inference status=%d forwarded=%q", rec.Code, forwarded)
	}
	var got map[string]any
	if err := json.Unmarshal(forwarded, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt-5.6-terra" || !reflect.DeepEqual(got["reasoning"], map[string]any{"effort": "medium"}) {
		t.Fatalf("model/reasoning changed: %s", forwarded)
	}
	if !reflect.DeepEqual(got["tools"], []any{map[string]any{"type": "web_search"}}) {
		t.Fatalf("hosted tool changed: %s", forwarded)
	}
	catalogs := inputToolCatalogs(got)
	if len(catalogs) != 1 || catalogs[0]["role"] != "developer" {
		t.Fatalf("additional_tools location changed: %s", forwarded)
	}
	defs := asSlice(asSlice(catalogs[0]["tools"])[0].(map[string]any)["tools"])
	if len(defs) != 1 || toolNameOf(defs[0].(map[string]any)) != "grep_files" {
		t.Fatalf("local selection not forwarded: %s", forwarded)
	}
	events, _, _, _ := s.Events().Snapshot(0)
	if len(events) != 1 || events[0].Reason == reasonNotLLMPath || events[0].Usage == nil || events[0].UsageMissing != "" {
		t.Fatalf("inference event=%+v", events)
	}
	u := events[0].Usage
	if u.InputTokens == nil || *u.InputTokens != 36 || u.OutputTokens == nil || *u.OutputTokens != 8 || u.CachedTokens == nil || *u.CachedTokens != 22 || u.ReasoningTokens == nil || *u.ReasoningTokens != 3 {
		t.Fatalf("upstream usage=%+v", u)
	}

	controlBody := `{"opaque":"unchanged"}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/telemetry", strings.NewReader(controlBody))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || string(control) != controlBody {
		t.Fatalf("control status=%d forwarded=%q", rec.Code, control)
	}
	events, _, _, _ = s.Events().Snapshot(0)
	if len(events) != 2 || events[1].Reason != reasonNotLLMPath || events[1].RequestPath != "/v1/telemetry" || events[1].UsageMissing != "no_usage" {
		t.Fatalf("control event=%+v", events)
	}
}
