package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// ServeFakeUpstream is a stand-in Responses API. Output tokens depend on whether
// the proxy replaced tool_choice, so a fake run shows routing on against off.
func ServeFakeUpstream(listen string) (origin string, closeFn func(), err error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return "", nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		_, isString := req["tool_choice"].(string)
		steered := !isString
		output := 400
		reasoning := 340
		if steered {
			output = 80
			reasoning = 30
		}
		usage := map[string]any{
			"input_tokens":          12000,
			"input_tokens_details":  map[string]any{"cached_tokens": 9000},
			"output_tokens":         output,
			"output_tokens_details": map[string]any{"reasoning_tokens": reasoning},
		}
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"usage": usage}})
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/v1", func() { _ = srv.Close() }, nil
}
