package proxy

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestUsageCollectorConnectEndStreamErrorAndUsage(t *testing.T) {
	c := newUsageCollector("application/connect+proto")
	var body []byte
	body = append(body, connectFrame(0, protoString(1, `{"usage":{"input_tokens":12,"output_tokens":4}}`))...)
	body = append(body, connectFrame(connectFlagEndStream, []byte(`{"error":{"code":"internal","message":"boom"}}`))...)
	c.Write(body)
	u, partial, missing, finish := c.Finish()
	if u == nil || u.InputTokens == nil || *u.InputTokens != 12 || u.OutputTokens == nil || *u.OutputTokens != 4 {
		t.Fatalf("usage=%+v", u)
	}
	if finish != "error" {
		t.Fatalf("finish=%q", finish)
	}
	if partial {
		t.Fatal("partial=true for complete stream")
	}
	if missing != "" {
		t.Fatalf("missing=%q", missing)
	}
}

func TestUsageCollectorConnectUsageLastWins(t *testing.T) {
	c := newUsageCollector("application/connect+proto")
	c.Write(connectFrame(0, protoString(1, `{"usage":{"input_tokens":1}}`)))
	c.Write(connectFrame(0, protoString(2, `{"usage":{"input_tokens":9,"output_tokens":7}}`)))
	u, _, missing, finish := c.Finish()
	if u == nil || u.InputTokens == nil || *u.InputTokens != 9 {
		t.Fatalf("usage=%+v", u)
	}
	if finish != "" || missing != "" {
		t.Fatalf("finish=%q missing=%q", finish, missing)
	}
}

func TestUsageCollectorConnectTruncatedFrameIsIncomplete(t *testing.T) {
	c := newUsageCollector("application/connect+proto")
	frame := connectFrame(0, protoString(1, `{"usage":{"input_tokens":3}}`))
	c.Write(frame[:len(frame)-2])
	_, _, _, finish := c.Finish()
	if finish != "incomplete" {
		t.Fatalf("finish=%q", finish)
	}
}

func TestUsageCollectorConnectNoFrames(t *testing.T) {
	c := newUsageCollector("application/connect+proto")
	_, _, missing, finish := c.Finish()
	if finish != "" {
		t.Fatalf("finish=%q", finish)
	}
	if missing != "no_usage" {
		t.Fatalf("missing=%q", missing)
	}
}

func TestUsageCollectorConnectGzipFrame(t *testing.T) {
	c := newUsageCollector("application/connect+proto")
	payload := protoString(1, `{"usage":{"input_tokens":5,"output_tokens":2}}`)
	body := connectFrame(connectFlagCompressed, gzipBytes(t, payload))
	c.Write(body)
	u, _, missing, finish := c.Finish()
	if u == nil || u.InputTokens == nil || *u.InputTokens != 5 {
		t.Fatalf("usage=%+v missing=%q", u, missing)
	}
	if finish != "" {
		t.Fatalf("finish=%q", finish)
	}
}

func TestUsageCollectorResponsesSSECache(t *testing.T) {
	c := newUsageCollector("application/json")
	body := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":36,\"output_tokens\":8,\"input_tokens_details\":{\"cached_tokens\":22},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n"
	c.Write([]byte(body))
	u, _, missing, _ := c.Finish()
	if missing != "" || u == nil || u.CachedTokens == nil || *u.CachedTokens != 22 {
		t.Fatalf("usage=%+v missing=%q", u, missing)
	}
	if u.InputTokens == nil || *u.InputTokens != 36 || u.OutputTokens == nil || *u.OutputTokens != 8 {
		t.Fatalf("tokens %+v", u)
	}
	if u.ReasoningTokens == nil || *u.ReasoningTokens != 3 {
		t.Fatalf("reasoning %+v", u)
	}
}

func TestUsageCollectorSSEWithoutUsage(t *testing.T) {
	c := newUsageCollector("text/event-stream")
	c.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
	u, _, missing, _ := c.Finish()
	if u != nil || missing != "no_usage" {
		t.Fatalf("usage=%+v missing=%q", u, missing)
	}
}

func TestUsageCollectorPlainProtoNotConnect(t *testing.T) {
	c := newUsageCollector("application/proto")
	if c.connect {
		t.Fatal("application/proto must not enable connect framing")
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
