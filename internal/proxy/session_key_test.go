package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestSessionKeyGroupsClaudeRequestsWithoutLeakingID(t *testing.T) {
	s, _ := testProxy(t, host.Claude, nil, DefaultOptions())
	for _, id := range []string{"opaque-parent-id", "opaque-parent-id", "opaque-child-id"} {
		body := `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"x"}],"metadata":{"user_id":"` + id + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d", rec.Code)
		}
	}
	events, _, _, _ := s.Events().Snapshot(0)
	if len(events) != 3 || events[0].SessionKey == "" || events[0].SessionKey != events[1].SessionKey || events[0].SessionKey == events[2].SessionKey {
		t.Fatalf("session groups lost: %+v", events)
	}
	for _, event := range events {
		if strings.Contains(event.SessionKey, "opaque") {
			t.Fatal("raw session identifier leaked")
		}
	}
}
