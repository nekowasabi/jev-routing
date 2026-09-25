package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

type gateway struct {
	srv      *proxy.Server
	http     *http.Server
	origin   string
	snapshot []byte
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
	Seq            int64      `json:"seq"`
	SessionKey     string     `json:"sessionKey"`
	Source         string     `json:"source"`
	Reason         string     `json:"reason"`
	Apply          string     `json:"apply"`
	Changed        bool       `json:"changed"`
	OriginalModel  string     `json:"originalModel"`
	SentModel      string     `json:"sentModel"`
	UpstreamStatus *int       `json:"upstreamStatus"`
	HeaderMs       *float64   `json:"headerMs"`
	BodyMs         *float64   `json:"bodyMs"`
	Usage          *dashUsage `json:"usage"`
	UsagePartial   bool       `json:"usagePartial"`
	UsageMissing   string     `json:"usageMissing"`
	Canceled       bool       `json:"canceled"`
	UpstreamFinish string     `json:"upstreamFinish"`
	JevCalls       int        `json:"jevCalls"`
	JevAttempts    []struct {
		Ms           float64 `json:"ms"`
		InputTokens  *int    `json:"inputTokens"`
		OutputTokens *int    `json:"outputTokens"`
		Cached       bool    `json:"cached"`
	} `json:"jevAttempts"`
	CompactApplied bool `json:"compactApplied"`
	CompactDropped int  `json:"compactDropped"`
	SavedTokens    *struct {
		CompactionInput int `json:"compactionInput"`
	} `json:"savedTokens"`
	ClearedToolUses    int `json:"clearedToolUses"`
	ClearedInputTokens int `json:"clearedInputTokens"`
}

type dashApplication struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	CapabilityID string `json:"capabilityId"`
	CallID       string `json:"callId"`
	HasResult    bool   `json:"hasResult"`
}

func (g *gateway) meter(tasks ...string) (RunRecord, error) {
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
	g.snapshot = append([]byte(nil), raw...)
	if res.StatusCode != 200 {
		return RunRecord{}, fmt.Errorf("dashboard %s: %s", res.Status, truncate(raw, 200))
	}
	var body struct {
		Events           []dashEvent       `json:"events"`
		Applications     []dashApplication `json:"applications"`
		EventsTruncated  bool              `json:"eventsTruncated"`
		HistoryTruncated bool              `json:"historyTruncated"`
		JevHTTP          *int              `json:"jevHTTP"`
		Router           struct {
			OldestSeq int64 `json:"oldestSeq"`
		} `json:"router"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RunRecord{}, fmt.Errorf("meter decode: %s: %w", truncate(raw, 300), err)
	}
	if body.EventsTruncated || body.HistoryTruncated || body.Router.OldestSeq > 1 {
		return RunRecord{}, fmt.Errorf("meter history truncated (oldestSeq=%d)", body.Router.OldestSeq)
	}
	var out RunRecord
	out.Modes = map[string]int{}
	out.ModelUsage = map[string]ModelUsage{}
	out.SessionUsage = map[string]SessionUsage{}
	models := map[string]bool{}
	var incomplete []string
	usageMissingIssues := 0
	hostInferences := 0
	for i, event := range body.Events {
		if event.Reason == "not_llm_path" {
			out.ControlRequests++
			continue
		}
		if event.Source == "jev" {
			switch event.Apply {
			case "filter", "forced", "advise", "direct":
				out.JevApplied++
			}
		}
		hostInferences++
		upstream := event.UsageMissing != "not_called"
		if upstream {
			out.Requests++
		}
		mode := "passthrough"
		if event.Apply != "" && event.Apply != "none" {
			mode = event.Apply
		} else if event.Changed {
			mode = "rewritten"
		}
		if upstream {
			out.Modes[mode]++
		}
		if event.Usage != nil && event.Usage.InputTokens != nil && event.Usage.OutputTokens != nil && !event.UsagePartial {
			out.Metered++
			out.Input += deref(event.Usage.InputTokens)
			out.Cached += deref(event.Usage.CachedTokens)
			out.CacheWrite += deref(event.Usage.CacheWriteTokens)
			out.Output += deref(event.Usage.OutputTokens)
			out.Reasoning += deref(event.Usage.ReasoningTokens)
		}
		if upstream && (event.Usage == nil || event.Usage.InputTokens == nil || event.Usage.OutputTokens == nil || event.UsagePartial || event.UsageMissing != "") {
			incomplete = append(incomplete, fmt.Sprintf("event %d: upstream usage incomplete (%s)", i+1, event.UsageMissing))
			if event.Usage == nil && !event.UsagePartial && (event.UsageMissing == "no_usage" || event.Canceled) {
				out.UnreportedUpstream++
				usageMissingIssues++
				if event.Canceled && event.UpstreamFinish == "canceled" {
					out.CanceledUnreported++
				}
			}
		}
		if upstream && event.UpstreamStatus != nil && *event.UpstreamStatus >= 400 {
			out.FailedRequests++
		}
		ms := 0.0
		if event.HeaderMs != nil {
			ms += *event.HeaderMs
		}
		if event.BodyMs != nil {
			ms += *event.BodyMs
		}
		if upstream {
			out.LLMSeconds += ms / 1000
		}
		out.JevCalls += event.JevCalls
		jevAttempts := 0
		for _, attempt := range event.JevAttempts {
			if attempt.Cached {
				continue
			}
			jevAttempts++
			out.JevInput += deref(attempt.InputTokens)
			out.JevOutput += deref(attempt.OutputTokens)
			out.JevSeconds += attempt.Ms / 1000
			if attempt.InputTokens == nil || attempt.OutputTokens == nil {
				incomplete = append(incomplete, fmt.Sprintf("event %d: Jev usage incomplete", i+1))
			}
		}
		if jevAttempts != event.JevCalls {
			incomplete = append(incomplete, fmt.Sprintf("event %d: Jev attempts %d != calls %d", i+1, jevAttempts, event.JevCalls))
		}
		if event.SessionKey == "" {
			if upstream {
				out.UnattributedRequests++
			}
		} else {
			session := out.SessionUsage[event.SessionKey]
			if upstream {
				session.Requests++
				if event.Usage != nil && event.Usage.InputTokens != nil && event.Usage.OutputTokens != nil && !event.UsagePartial {
					session.Input += deref(event.Usage.InputTokens)
					session.Cached += deref(event.Usage.CachedTokens)
					session.CacheWrite += deref(event.Usage.CacheWriteTokens)
					session.Output += deref(event.Usage.OutputTokens)
				}
			}
			session.JevCalls += event.JevCalls
			for _, attempt := range event.JevAttempts {
				if !attempt.Cached {
					session.JevInput += deref(attempt.InputTokens)
					session.JevOutput += deref(attempt.OutputTokens)
				}
			}
			out.SessionUsage[event.SessionKey] = session
		}
		if event.CompactApplied {
			out.CompactRequests++
		}
		out.CompactDropped += event.CompactDropped
		if event.SavedTokens != nil {
			out.CompactSavedTokens += event.SavedTokens.CompactionInput
		}
		out.ClearedToolUses += event.ClearedToolUses
		out.ClearedInputTokens += event.ClearedInputTokens
		if upstream {
			model := event.SentModel
			if model == "" {
				model = event.OriginalModel
			}
			if model != "" {
				models[model] = true
				usage := out.ModelUsage[model]
				usage.Requests++
				if event.Usage != nil && event.Usage.InputTokens != nil && event.Usage.OutputTokens != nil && !event.UsagePartial {
					usage.Input += deref(event.Usage.InputTokens)
					usage.Cached += deref(event.Usage.CachedTokens)
					usage.CacheWrite += deref(event.Usage.CacheWriteTokens)
					usage.Output += deref(event.Usage.OutputTokens)
				}
				out.ModelUsage[model] = usage
			}
		}
	}
	subagents := map[string]bool{}
	for _, app := range body.Applications {
		if app.Source == "jev" && app.Kind == "skill" && app.State == "delivered" {
			out.JevApplied++
		}
		if app.Kind == "subagent" && isChildLaunch(app.CapabilityID) && app.State == "verified" && app.HasResult && app.CallID != "" && !subagents[app.CallID] {
			subagents[app.CallID] = true
			out.SubagentCalls++
		}
		if app.Kind != "skill" && (app.State == "started" || app.State == "result_received" || app.State == "verified") && (app.CallID == "" || !app.HasResult) {
			incomplete = append(incomplete, "tool call ID or result missing")
		}
	}
	if len(tasks) > 0 && tasks[0] == "dual-facts" {
		calls := map[string]string{}
		for _, app := range body.Applications {
			if app.Kind != "mcp_tool" || app.State != "verified" || !app.HasResult || app.CallID == "" {
				continue
			}
			parts := strings.SplitN(app.CapabilityID, ":", 3)
			if len(parts) != 3 || parts[0] != "mcp_tool" {
				continue
			}
			namePart, _, _ := strings.Cut(parts[2], "@")
			for _, name := range []string{"bench_left_fact", "bench_right_fact"} {
				if namePart == name || namePart == "mcp__bench__"+name {
					calls[name] = app.CallID
				}
			}
		}
		left, right := calls["bench_left_fact"], calls["bench_right_fact"]
		out.EvidenceComplete = left != "" && right != "" && left != right
	}
	if len(tasks) > 0 && (tasks[0] == "child-facts" || tasks[0] == "child-survey") {
		out.EvidenceComplete = out.SubagentCalls > 0
	}
	for model := range models {
		out.Models = append(out.Models, model)
	}
	if hostInferences == 0 {
		incomplete = append(incomplete, "no inference requests recorded")
	}
	if body.JevHTTP != nil && *body.JevHTTP != out.JevCalls {
		incomplete = append(incomplete, fmt.Sprintf("Jev HTTP requests %d != event calls %d", *body.JevHTTP, out.JevCalls))
	}
	out.OnlyUsageMissing = usageMissingIssues > 0 && usageMissingIssues == len(incomplete)
	if len(incomplete) > 0 {
		return out, fmt.Errorf("meter incomplete: %s", strings.Join(incomplete, "; "))
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
