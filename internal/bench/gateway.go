package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

type gateway struct {
	srv    *proxy.Server
	http   *http.Server
	origin string
}

func routingMode(on bool, onMode string) string {
	if !on {
		return proxy.ModeBaseline
	}
	if onMode == "" {
		return proxy.ModeFilter
	}
	return onMode
}

// startGateway serves one fresh proxy for a single run. upstreamEnv, when set,
// is applied only while the upstream URL is captured, then restored.
func startGateway(h host.ID, listen, mode, runID string, logW io.Writer, upstreamEnv map[string]string) (*gateway, error) {
	opt, err := proxy.OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	opt.Mode = mode
	if runID != "" {
		opt.RunID = runID
	}
	client := jev.FromEnv()
	if mode != proxy.ModeBaseline {
		if _, err := jev.StartupStatus(client, opt.SelectionMode); err != nil {
			return nil, err
		}
	}
	ln, err := listenBench(listen)
	if err != nil {
		return nil, err
	}
	// Why: the dashboard allows only the port the proxy was constructed with.
	// Port 0 is chosen by the kernel, so construct the proxy after bind.
	actual := ln.Addr().String()
	var srv *proxy.Server
	err = withEnv(upstreamEnv, func() error {
		var e error
		srv, e = proxy.NewWithOptions(actual, h, client, logW, opt)
		return e
	})
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	httpSrv := &http.Server{
		Handler:           h2c.NewHandler(srv.Handler(), &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() { _ = httpSrv.Serve(ln) }()
	origin := "http://" + ln.Addr().String()
	if err := waitHealthy(origin + "/healthz"); err != nil {
		_ = httpSrv.Close()
		return nil, err
	}
	return &gateway{srv: srv, http: httpSrv, origin: origin}, nil
}

func (g *gateway) Close() {
	if g != nil && g.http != nil {
		_ = g.http.Close()
	}
}

func withEnv(kv map[string]string, fn func() error) error {
	type saved struct {
		key string
		val string
		ok  bool
	}
	var prev []saved
	for k, v := range kv {
		old, ok := os.LookupEnv(k)
		prev = append(prev, saved{k, old, ok})
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	defer func() {
		for _, p := range prev {
			if p.ok {
				_ = os.Setenv(p.key, p.val)
			} else {
				_ = os.Unsetenv(p.key)
			}
		}
	}()
	return fn()
}

func listenBench(listen string) (net.Listener, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		ln, err := net.Listen("tcp", listen)
		if err == nil {
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitHealthy(url string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not become healthy at %s", url)
}

type dashUsage struct {
	InputTokens      *int `json:"inputTokens"`
	OutputTokens     *int `json:"outputTokens"`
	CachedTokens     *int `json:"cachedTokens"`
	CacheWriteTokens *int `json:"cacheWriteTokens"`
	ReasoningTokens  *int `json:"reasoningTokens"`
}

type dashEvent struct {
	Apply          string     `json:"apply"`
	Changed        bool       `json:"changed"`
	OriginalModel  string     `json:"originalModel"`
	SentModel      string     `json:"sentModel"`
	UpstreamStatus *int       `json:"upstreamStatus"`
	HeaderMs       *float64   `json:"headerMs"`
	BodyMs         *float64   `json:"bodyMs"`
	Usage          *dashUsage `json:"usage"`
	JevCalls       int        `json:"jevCalls"`
	JevAttempts    []struct {
		Ms          float64 `json:"ms"`
		InputTokens *int    `json:"inputTokens"`
	} `json:"jevAttempts"`
}

func (g *gateway) meter() (RunRecord, error) {
	// Requests are logged when their reply ends; give the last one a moment to land.
	time.Sleep(1500 * time.Millisecond)
	res, err := http.Get(g.origin + "/dashboard/events")
	if err != nil {
		return RunRecord{}, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return RunRecord{}, err
	}
	if res.StatusCode != 200 {
		return RunRecord{}, fmt.Errorf("dashboard %s: %s", res.Status, truncate(raw, 200))
	}
	var body struct {
		Events []dashEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RunRecord{}, fmt.Errorf("meter decode: %s: %w", truncate(raw, 300), err)
	}
	var out RunRecord
	out.Modes = map[string]int{}
	models := map[string]bool{}
	for _, event := range body.Events {
		out.Requests++
		mode := "passthrough"
		if event.Apply != "" && event.Apply != "none" {
			mode = event.Apply
		} else if event.Changed {
			mode = "rewritten"
		}
		out.Modes[mode]++
		if event.Usage != nil {
			out.Metered++
			out.Input += deref(event.Usage.InputTokens)
			out.Cached += deref(event.Usage.CachedTokens)
			out.CacheWrite += deref(event.Usage.CacheWriteTokens)
			out.Output += deref(event.Usage.OutputTokens)
			out.Reasoning += deref(event.Usage.ReasoningTokens)
		}
		if event.UpstreamStatus != nil && *event.UpstreamStatus >= 400 {
			out.FailedRequests++
		}
		ms := 0.0
		if event.HeaderMs != nil {
			ms += *event.HeaderMs
		}
		if event.BodyMs != nil {
			ms += *event.BodyMs
		}
		out.LLMSeconds += ms / 1000
		out.JevCalls += event.JevCalls
		for _, attempt := range event.JevAttempts {
			out.JevInput += deref(attempt.InputTokens)
			out.JevSeconds += attempt.Ms / 1000
		}
		if event.SentModel != "" {
			models[event.SentModel] = true
		} else if event.OriginalModel != "" {
			models[event.OriginalModel] = true
		}
	}
	for model := range models {
		out.Models = append(out.Models, model)
	}
	return out, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
