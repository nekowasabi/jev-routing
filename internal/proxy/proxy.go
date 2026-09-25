package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const maxRequestBodyBytes = 16 << 20

type Server struct {
	Listen      string
	Host        host.ID
	Upstream    *url.URL
	Client      *jev.Client
	Log         *log.Logger
	Options     Options
	mu          sync.Mutex
	catalogMu   sync.RWMutex
	applyMu     sync.Mutex
	Last        RewriteStats
	Requests    int
	CharsBefore int
	CharsAfter  int
	events      *EventLog
	publicBind  bool
	listenPort  string
	saveErr     error

	Reached             int
	Rewritten           int
	Passthrough         int
	JevHTTP             int
	JevOK               int
	JevFail             int
	JevCacheHits        int
	TotalRequests       int
	SelectionApplied    int
	CompactionApplied   int
	SelectionSources    map[string]int
	ApplicationModes    map[string]int
	RequestRoutes       map[string]int
	RequestContentTypes map[string]int

	Catalog       plan.Catalog
	SkillBodies   map[string]string
	Executor      HostExecutor
	Apps          *AppStore
	ApplyErr      string
	LastDelivered string
}

func (s *Server) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Requests
}

func (s *Server) RoutingChars() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CharsBefore, s.CharsAfter
}

func (s *Server) Events() *EventLog { return s.events }

func (s *Server) StatsSnapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"host":                s.Last.Host,
		"toolBefore":          s.Last.ToolBefore,
		"toolAfter":           s.Last.ToolAfter,
		"chosen":              s.Last.Chosen,
		"done":                s.Last.Done,
		"gated":               s.Last.Gated,
		"charsBefore":         s.CharsBefore,
		"charsAfter":          s.CharsAfter,
		"compactDropped":      s.Last.CompactDropped,
		"engine":              s.Last.Engine,
		"requests":            s.Requests,
		"instanceId":          s.events.InstanceID,
		"startedAt":           s.events.StartedAt,
		"mode":                s.Options.Mode,
		"compaction":          s.Options.Compaction,
		"reasoning":           s.Options.Reasoning,
		"selectionMode":       s.Options.SelectionMode,
		"runId":               s.Options.RunID,
		"reached":             s.Reached,
		"rewritten":           s.Rewritten,
		"passthrough":         s.Passthrough,
		"jevHTTP":             s.JevHTTP,
		"jevOK":               s.JevOK,
		"jevFail":             s.JevFail,
		"jevCacheHits":        s.JevCacheHits,
		"totalRequests":       s.TotalRequests,
		"selectionApplied":    s.SelectionApplied,
		"compactionApplied":   s.CompactionApplied,
		"selectionSources":    copyCounts(s.SelectionSources),
		"applicationModes":    copyCounts(s.ApplicationModes),
		"requestRoutes":       copyCounts(s.RequestRoutes),
		"requestContentTypes": copyCounts(s.RequestContentTypes),
	}
}

func (s *Server) RunStats() map[string]any {
	snap := s.StatsSnapshot()
	events, _, _, truncated := s.events.Snapshot(0)
	apps := s.Apps.Snapshot()
	s.applyMu.Lock()
	lastDelivered, applyErr := s.LastDelivered, s.ApplyErr
	s.applyMu.Unlock()
	// Compatible keys first.
	return map[string]any{
		"requests":            snap["requests"],
		"charsBefore":         snap["charsBefore"],
		"charsAfter":          snap["charsAfter"],
		"instanceId":          snap["instanceId"],
		"mode":                snap["mode"],
		"compaction":          snap["compaction"],
		"reasoning":           snap["reasoning"],
		"selectionMode":       snap["selectionMode"],
		"runId":               snap["runId"],
		"reached":             snap["reached"],
		"rewritten":           snap["rewritten"],
		"passthrough":         snap["passthrough"],
		"jevHTTP":             snap["jevHTTP"],
		"jevOK":               snap["jevOK"],
		"jevFail":             snap["jevFail"],
		"jevCacheHits":        snap["jevCacheHits"],
		"scope":               "single-process",
		"totalRequests":       snap["totalRequests"],
		"selectionApplied":    snap["selectionApplied"],
		"compactionApplied":   snap["compactionApplied"],
		"selectionSources":    snap["selectionSources"],
		"applicationModes":    snap["applicationModes"],
		"requestRoutes":       snap["requestRoutes"],
		"requestContentTypes": snap["requestContentTypes"],
		"events":              events,
		"selectionJevTokens":  selectionJevTokens(events),
		"eventsTruncated":     truncated || snap["requests"].(int) > len(events),
		"lastDelivered":       lastDelivered,
		"applyErr":            applyErr,
		"applications":        publicApplications(apps),
		"class_map":           ClassMap(apps, events, s.Options, string(s.Host)),
	}
}

func publicApplications(apps []*Application) []map[string]any {
	out := make([]map[string]any, 0, len(apps))
	for _, a := range apps {
		if a == nil {
			continue
		}
		row := map[string]any{
			"decisionId":    a.DecisionID,
			"source":        a.Source,
			"state":         a.State,
			"kind":          a.Kind,
			"capabilityId":  a.CapabilityID,
			"callId":        a.CallID,
			"deliveredHash": a.DeliveredHash,
			"verified":      a.Verified,
			"hasResult":     a.Result != "",
		}
		if a.Host != "" {
			row["host"] = a.Host
		}
		if a.PluginOf != "" {
			row["pluginOf"] = a.PluginOf
		}
		out = append(out, row)
	}
	return out
}

func usageInputTokens(u *jev.Usage) *int {
	if u == nil {
		return nil
	}
	return u.InputTokens
}

func usageOutputTokens(u *jev.Usage) *int {
	if u == nil {
		return nil
	}
	return u.OutputTokens
}

// selectionJevTokens keeps an unreported service value distinct from zero.
func selectionJevTokens(events []Event) map[string]any {
	input, output, calls := 0, 0, 0
	for _, event := range events {
		for _, attempt := range event.JevAttempts {
			if attempt.Purpose != "selection" || attempt.Cached {
				continue
			}
			calls++
			if attempt.InputTokens == nil || attempt.OutputTokens == nil {
				return map[string]any{"missing": true, "reason": "not_reported", "calls": calls}
			}
			input += *attempt.InputTokens
			output += *attempt.OutputTokens
		}
	}
	if calls == 0 {
		return map[string]any{"missing": true, "reason": "no_selection_calls", "calls": 0}
	}
	return map[string]any{"missing": false, "inputTokens": input, "outputTokens": output, "calls": calls}
}

func copyCounts(src map[string]int) map[string]int {
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func copyKindModes(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func DefaultUpstream(h host.ID) string {
	switch h {
	case host.Claude:
		if u := os.Getenv("ANTHROPIC_UPSTREAM"); u != "" {
			return u
		}
		return "https://api.anthropic.com"
	case host.Codex:
		if u := os.Getenv("CODEX_UPSTREAM"); u != "" {
			return u
		}
		return "https://chatgpt.com/backend-api/codex"
	case host.Devin:
		if u := os.Getenv("DEVIN_UPSTREAM"); u != "" {
			return u
		}
		return "https://server.codeium.com"
	default:
		if u := os.Getenv("GROK_OAUTH_UPSTREAM"); u != "" {
			return u
		}
		return "https://cli-chat-proxy.grok.com"
	}
}

func New(listen string, h host.ID, client *jev.Client, logWriter io.Writer) (*Server, error) {
	return NewWithOptions(listen, h, client, logWriter, DefaultOptions())
}

func NewWithOptions(listen string, h host.ID, client *jev.Client, logWriter io.Writer, opt Options) (*Server, error) {
	u, err := url.Parse(DefaultUpstream(h))
	if err != nil {
		return nil, err
	}
	if logWriter == nil {
		logWriter = os.Stderr
	}
	if opt.Mode == "" {
		opt = DefaultOptions()
	}
	if opt.RunID == "" {
		opt.RunID = newInstanceID()
	}
	srv := &Server{
		Listen:     listen,
		Host:       h,
		Upstream:   u,
		Client:     client,
		Log:        log.New(logWriter, "jev-routing ", log.LstdFlags),
		Options:    opt,
		events:     newEventLog(),
		publicBind: listenIsPublic(listen),
		listenPort: listenPortOf(listen),
		Apps:       NewAppStore(),
	}
	if opt.AutoApply && srv.Executor == nil {
		srv.Executor = &recordingExec{}
	}
	if err := srv.loadEnvCatalog(); err != nil {
		return nil, err
	}
	srv.bindAfterRewrite()
	return srv, nil
}

func (s *Server) loadEnvCatalog() error {
	if s == nil {
		return nil
	}
	if path := strings.TrimSpace(os.Getenv("JEV_CATALOG")); path != "" {
		cat, err := plan.LoadCatalog(path)
		if err != nil {
			return err
		}
		s.Catalog.Merge(cat.Items)
	}
	if dir := strings.TrimSpace(os.Getenv("JEV_SKILL_DIR")); dir != "" {
		inv, bodies, err := plan.InventoryFromSkillDir(dir)
		if err != nil {
			return err
		}
		cat, err := plan.BuildCatalog(inv, s.Host, &plan.LaunchProbe{})
		if err != nil {
			return err
		}
		s.Catalog.Merge(cat.Items)
		if s.SkillBodies == nil {
			s.SkillBodies = map[string]string{}
		}
		for ref, body := range bodies {
			if s.SkillBodies[ref] == "" {
				s.SkillBodies[ref] = body
			}
		}
	}
	return nil
}

func (s *Server) bindAfterRewrite() {
	if s == nil || !s.Options.AutoApply {
		return
	}
	s.Options.AfterRewrite = func(ctx context.Context, body []byte) []byte {
		extra := s.autoApplyContext(ctx, body)
		if extra == "" {
			return body
		}
		out, err := ApplyHostContext(s.Host, body, extra)
		if err != nil {
			if s.Options.ApplicationPolicy == PolicyRequired {
				s.applyMu.Lock()
				s.ApplyErr = err.Error()
				s.applyMu.Unlock()
			}
			return body
		}
		return out
	}
}

type recordingExec struct {
	mu     sync.Mutex
	skills []string
	n      int
}

func (r *recordingExec) DeliverSkill(_ string, body, _ string) error {
	r.mu.Lock()
	r.skills = append(r.skills, body)
	r.mu.Unlock()
	return nil
}

func (r *recordingExec) StartCall(call HostCall) (string, error) {
	r.mu.Lock()
	r.n++
	id := fmt.Sprintf("call-%d", r.n)
	r.mu.Unlock()
	_ = call
	return id, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		if s.publicBind {
			http.Error(w, "stats are available only on loopback", http.StatusForbidden)
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(s.StatsSnapshot())
	})
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/dashboard/", s.handleDashboard)
	mux.HandleFunc("/dashboard/events", s.handleDashboardEvents)
	proxy := httputil.NewSingleHostReverseProxy(s.Upstream)
	orig := proxy.Director
	proxy.Director = func(r *http.Request) {
		if s.Host == host.Codex && strings.HasPrefix(s.Upstream.Path, "/backend-api/codex") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1")
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/v1")
		}
		orig(r)
		r.Host = s.Upstream.Host
		r.Header.Set("host", s.Upstream.Host)
		r.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		seq, _ := res.Request.Context().Value(eventSeqKey{}).(int64)
		started, _ := res.Request.Context().Value(reqStartKey{}).(time.Time)
		headerMs := time.Since(started).Seconds() * 1000
		status := res.StatusCode
		s.events.Update(seq, func(e *Event) {
			e.UpstreamStatus = &status
			e.HeaderMs = &headerMs
		})
		// Why: mirrors jev-gateway app.js:138-152 -- a rewritten Codex request
		// upstream rejected (400/422) is retried exactly once with the exact
		// original body, so the router is never the reason a request fails.
		// Must run before the body-observing wraps below so they see the
		// response actually delivered to the client, not the rejected one.
		if retry, ok := res.Request.Context().Value(codexSteerRetryCtxKey{}).(*codexSteerRetry); ok && retry != nil &&
			(res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnprocessableEntity) {
			if resent, rerr := s.resendCodexOriginal(res.Request, retry.original); rerr == nil {
				_ = res.Body.Close()
				*res = *resent
				newStatus := res.StatusCode
				reason := "upstream_rejected_" + retry.mode
				s.events.Update(retry.seq, func(e *Event) {
					e.Reason = reason
					e.Chosen = "passthrough:" + reason
					e.Apply = applyNone
					e.UpstreamStatus = &newStatus
				})
				status = newStatus
			} else {
				s.Log.Printf("codex-steer: resend of original body after upstream %d failed: %v", res.StatusCode, rerr)
			}
		}
		ct := res.Header.Get("content-type")
		if strings.Contains(strings.ToLower(ct), "proto") {
			res.Body = wrapProtoHosts(res.Body, func(hosts []string) {
				if len(hosts) == 0 {
					return
				}
				s.events.Update(seq, func(e *Event) {
					e.URLHosts = mergeHosts(e.URLHosts, hosts)
				})
			})
		}
		res.Body = wrapBodyObserve(res.Body, func(raw []byte) {
			s.observeResponse(raw)
			if tools := extractObservedTools(raw); len(tools) > 0 {
				s.events.Update(seq, func(e *Event) {
					e.ObservedTools = mergeObservedTools(e.ObservedTools, tools)
					markObservedCoverage(e)
				})
			}
		})
		if s.Host == host.Claude && s.Options.ClaudeClearToolUses {
			res.Body = wrapClearEdits(res.Body, ct, func(toolUses, inputTokens int, found bool) {
				if !found {
					return
				}
				s.events.Update(seq, func(e *Event) {
					e.ClearedToolUses = toolUses
					e.ClearedInputTokens = inputTokens
				})
			})
		}
		gateKey, _ := res.Request.Context().Value(clearGateCtxKey{}).(string)
		res.Body = wrapUsage(res.Body, ct, func(u *NormalizedUsage, partial bool, missing, finish string) {
			bodyMs := time.Since(started).Seconds() * 1000
			s.Options.clearGates.observe(gateKey, u)
			s.events.Update(seq, func(e *Event) {
				e.Usage = u
				e.UsagePartial = partial
				e.UsageMissing = missing
				e.BodyMs = &bodyMs
				if finish != "" {
					e.UpstreamFinish = finish
				} else if e.UpstreamFinish == "" {
					e.UpstreamFinish = "complete"
				}
			})
		})
		return nil
	}
	proxy.ErrorLog = s.Log
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		seq, _ := r.Context().Value(eventSeqKey{}).(int64)
		if errors.Is(err, context.Canceled) {
			s.events.Update(seq, func(e *Event) {
				e.Canceled = true
				e.UpstreamFinish = "canceled"
			})
			return
		}
		s.events.Update(seq, func(e *Event) {
			e.UpstreamFinish = "error"
		})
		s.Log.Printf("proxy error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.TotalRequests++
		if s.RequestRoutes == nil {
			s.RequestRoutes = map[string]int{}
		}
		s.RequestRoutes[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		if r.Method == http.MethodPost && looksDevinInference(r.URL.Path) {
			ct := clipEvent(r.Header.Get("Content-Type"))
			s.mu.Lock()
			s.Requests++
			s.Reached++
			s.Passthrough++
			if s.RequestContentTypes == nil {
				s.RequestContentTypes = map[string]int{}
			}
			s.RequestContentTypes[r.Method+" "+r.URL.Path+" "+ct]++
			s.mu.Unlock()
			bodyBytes := 0
			if r.ContentLength > 0 {
				bodyBytes = int(r.ContentLength)
			}
			ev := s.events.Add(Event{
				Host:        string(s.Host),
				Reason:      reasonStream,
				RequestPath: r.URL.Path,
				Method:      r.Method,
				ContentType: ct,
				BodyBytes:   bodyBytes,
			})
			ctx := context.WithValue(r.Context(), eventSeqKey{}, ev.Seq)
			ctx = context.WithValue(ctx, reqStartKey{}, time.Now())
			ctx = jev.WithAttemptHook(ctx, s.connectJevAttemptHook(ev.Seq))
			r = r.WithContext(ctx)
			if looksDevinInference(r.URL.Path) && connectContentType(ct) {
				r.Body = wrapConnectDevinBody(r.Body, s, ev.Seq, r.Context())
				r.ContentLength = -1
				r.Header.Del("Content-Length")
			}
		} else if r.Method == http.MethodPost && looksLikeLLM(r.URL.Path) {
			ct := clipEvent(r.Header.Get("Content-Type"))
			s.mu.Lock()
			s.Requests++
			s.Reached++
			if s.RequestContentTypes == nil {
				s.RequestContentTypes = map[string]int{}
			}
			s.RequestContentTypes[r.Method+" "+r.URL.Path+" "+ct]++
			s.mu.Unlock()
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			origBytes := len(raw)
			origJSON := json.Valid(raw)
			nativeKind := ""
			if origJSON {
				nativeKind = nativeCompactionKindJSON(raw, s.Host)
			}
			sessionKey := sessionKeyFromBody(raw, s.events.InstanceID)
			shape := catalogShape(raw)
			var urlHosts []string
			if !origJSON {
				urlHosts = protoURLHosts(raw)
			}
			var attemptsMu sync.Mutex
			var attempts []JevAttempt
			var jevHTTP, jevOK, jevFail, jevCache int
			ctx := jev.WithAttemptHook(r.Context(), func(a jev.Attempt) {
				attemptsMu.Lock()
				attempts = append(attempts, JevAttempt{
					Purpose: a.Purpose, Ms: a.Duration.Seconds() * 1000,
					OK: a.OK, Cached: a.Cached, ErrKind: a.ErrKind, Status: a.Status, Questions: a.Questions,
					InputTokens: usageInputTokens(a.Usage), OutputTokens: usageOutputTokens(a.Usage),
				})
				if a.Cached {
					jevCache++
					attemptsMu.Unlock()
					return
				}
				jevHTTP++
				if a.OK {
					jevOK++
				} else {
					jevFail++
				}
				attemptsMu.Unlock()
			})
			stats := RewriteStats{}
			// Why: kept across the native block so the later (forwarded) event
			// construction can record why a native-compaction candidate ended
			// up going upstream instead of being synthesized -- see
			// docs/MEMO.md "圧縮の同一経路指標".
			compactForwardReason := ""
			nativeEnabled := nativeCompactionEnabled(s.Options)
			if !nativeEnabled && nativeKind != "" {
				compactForwardReason = "native_compaction_disabled"
			}
			if err == nil && origJSON && nativeEnabled {
				kind := nativeKind
				// Why: Codex's host summary keeps task progress; the retained
				// transcript still caused repeated compaction in real CLI runs.
				if kind == "codex" && !s.Options.CodexNativeCompaction {
					kind = ""
					compactForwardReason = "native_compaction_off"
				}
				var text string
				var nstats RewriteStats
				if kind != "" {
					text, nstats = retainNative(ctx, raw, s.Client, s.Options, kind)
				}
				if kind == "claude" {
					// Like fast-jev-compaction, let Claude Code's own summary run instead.
					if why := claudeFallback(nstats); why != "" {
						s.Log.Printf("fast-jev-native: claude compaction forwarded upstream: %s", why)
						kind = ""
						compactForwardReason = why
					}
				}
				if kind == "codex" {
					if why := codexFallback(text, nstats); why != "" {
						s.Log.Printf("fast-jev-native: codex compaction forwarded upstream: %s", why)
						kind = ""
						compactForwardReason = why
					}
				}
				if kind != "" {
					stats = nstats
					s.mu.Lock()
					s.Last = stats
					s.CharsBefore += len(raw)
					s.CharsAfter += len(text)
					s.Rewritten++
					s.CompactionApplied++
					s.JevHTTP += jevHTTP
					s.JevOK += jevOK
					s.JevFail += jevFail
					s.JevCacheHits += jevCache
					s.mu.Unlock()
					s.Log.Print(FormatStats(stats))
					ev := EventFromStats(stats)
					ev.NativeCompactionRequested = nativeKind != ""
					ev.CompactRoute = "synthetic"
					apparent := compactUsage(text)
					if v, ok := apparent["input_tokens"].(int); ok {
						ev.CompactApparentInput = v
					}
					if v, ok := apparent["output_tokens"].(int); ok {
						ev.CompactApparentOutput = v
					}
					ev.CompactSummaryBytes = len(text)
					ev.SessionKey = sessionKey
					ev.RequestPath = r.URL.Path
					ev.Method = r.Method
					ev.ContentType = ct
					ev.BodyBytes = origBytes
					ev.JsonValid = &origJSON
					ev.URLHosts = urlHosts
					ev.Catalog = shape
					ev.JevAttempts = attempts
					ev.JevCalls = jevHTTP
					ev.SelectionJevCalls = selectionJevCalls(attempts)
					ev.OtherJevCalls = jevHTTP - selectionJevCalls(attempts)
					ev.JevCached = jevCache
					ev.JevFailed = jevFail
					ev = s.events.Add(ev)
					s.events.Update(ev.Seq, func(e *Event) {
						e.UpstreamFinish = "fast-jev-native"
						zero := 0
						e.UpstreamStatus = &zero
						e.UsageMissing = "not_called"
					})
					writeNativeCompaction(w, r, s.Host, stats.SentModel, text, nativeStream(raw, r, s.Host))
					return
				}
			}
			// Why: the gate asks Jev before RewriteWith instead of next to the
			// edit below. Reason: RewriteWith's branch folds this request's Jev
			// attempts into s.JevHTTP, keeping the dashboard total equal to the
			// sum of event JevCalls that the bench meter checks.
			gateClear := true
			var gateKey, gateState, gateReason string
			if origJSON && s.Host == host.Claude && s.Options.ClaudeClearToolUses && s.Options.ClaudeClearGate == ClearGateJev {
				gateKey, gateClear, gateState, gateReason = s.Options.clearGates.gate(ctx, s.Client, raw, s.Options)
				if gateKey != "" {
					ctx = context.WithValue(ctx, clearGateCtxKey{}, gateKey)
				}
			}
			var codexSteerOriginal []byte
			if err == nil && json.Valid(raw) {
				rewritten, st, rerr := RewriteWith(ctx, raw, s.Host, s.Client, s.Options)
				stats = st
				// Why: codex-steer's upstream-rejection recovery (mirrors
				// jev-gateway app.js:138-152) needs the exact bytes the client
				// sent, captured before `raw` below is reassigned to the
				// rewritten body. Only kept for requests this path actually
				// rewrote; see codex_steer.go and the ModifyResponse hook.
				if s.Host == host.Codex && s.Options.CodexSteer && (stats.Apply == applyForced || stats.Apply == applyCodexNone) {
					codexSteerOriginal = append([]byte(nil), raw...)
				}
				if rerr == nil {
					s.mu.Lock()
					s.Last = stats
					s.CharsBefore += len(raw)
					if stats.Changed {
						s.Rewritten++
					} else {
						s.Passthrough++
					}
					if stats.Apply != "" && stats.Apply != applyNone {
						s.SelectionApplied++
						if s.SelectionSources == nil {
							s.SelectionSources = map[string]int{}
						}
						if s.ApplicationModes == nil {
							s.ApplicationModes = map[string]int{}
						}
						s.SelectionSources[stats.Source]++
						s.ApplicationModes[stats.Apply]++
					}
					if stats.CompactApplied {
						s.CompactionApplied++
					}
					s.mu.Unlock()
					s.Log.Print(FormatStats(stats))
					extra := s.autoApplyContext(ctx, raw)
					s.mu.Lock()
					s.JevHTTP += jevHTTP
					s.JevOK += jevOK
					s.JevFail += jevFail
					s.JevCacheHits += jevCache
					s.mu.Unlock()
					s.observeLastUserResult(raw)
					if extra != "" {
						if withCtx, err := ApplyHostContext(s.Host, rewritten, extra); err == nil {
							rewritten = withCtx
						} else if s.Options.ApplicationPolicy == PolicyRequired {
							s.applyMu.Lock()
							s.ApplyErr = err.Error()
							s.applyMu.Unlock()
						}
					}
					raw = rewritten
					s.mu.Lock()
					s.CharsAfter += len(raw)
					s.mu.Unlock()
				} else {
					s.Log.Printf("rewrite skipped: %v", rerr)
				}
			} else {
				stats.Reason = reasonNotJSON
				stats.Chosen = "passthrough:" + reasonNotJSON
				s.mu.Lock()
				s.Passthrough++
				s.CharsBefore += len(raw)
				s.CharsAfter += len(raw)
				s.mu.Unlock()
			}
			// Why: applied independently of RewriteWith's outcome (including the
			// claude_advise_disabled early return, rewrite.go's reasonClaudeAdviseOff)
			// so the clear-tool-uses condition does not require advise to be on too.
			if origJSON && s.Host == host.Claude && gateClear {
				if edited, ok := applyClaudeClearToolUses(raw, r.Header, s.Options); ok {
					raw = edited
				}
			}
			ev := EventFromStats(stats)
			ev.NativeCompactionRequested = nativeKind != ""
			if ev.NativeCompactionRequested {
				ev.CompactRoute = "forwarded"
				ev.CompactForwardReason = compactForwardReason
			}
			ev.SessionKey = sessionKey
			ev.RequestPath = r.URL.Path
			ev.Method = r.Method
			ev.ContentType = ct
			ev.BodyBytes = origBytes
			ev.JsonValid = &origJSON
			ev.URLHosts = urlHosts
			ev.Catalog = shape
			ev.JevAttempts = attempts
			ev.JevCalls = jevHTTP
			ev.SelectionJevCalls = selectionJevCalls(attempts)
			ev.OtherJevCalls = jevHTTP - selectionJevCalls(attempts)
			ev.JevCached = jevCache
			ev.JevFailed = jevFail
			ev.ClearGate = gateState
			ev.ClearGateReason = gateReason
			ev = s.events.Add(ev)
			if stats.Direct && stats.DirectName != "" {
				s.writeDirect(w, r, stats)
				s.events.Update(ev.Seq, func(e *Event) {
					e.UpstreamFinish = "direct"
					zero := 0
					e.UpstreamStatus = &zero
					e.UsageMissing = "not_called"
					e.SavedTokens = savedTokens(raw, stats, true)
				})
				return
			}
			if saved := savedTokens(raw, stats, false); saved != nil {
				s.events.Update(ev.Seq, func(e *Event) { e.SavedTokens = saved })
			}
			ctx = context.WithValue(ctx, eventSeqKey{}, ev.Seq)
			ctx = context.WithValue(ctx, reqStartKey{}, time.Now())
			if codexSteerOriginal != nil {
				mode := "forced"
				if stats.Apply == applyCodexNone {
					mode = "none"
				}
				ctx = context.WithValue(ctx, codexSteerRetryCtxKey{}, &codexSteerRetry{original: codexSteerOriginal, mode: mode, seq: ev.Seq})
			}
			r = r.WithContext(ctx)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			r.Header.Set("Content-Length", itoa(len(raw)))
		} else if r.Method == http.MethodPost {
			// Why: Control RPCs share the POST surface; record path/type only,
			// without buffering or rewriting bodies that are not model inference.
			ct := clipEvent(r.Header.Get("Content-Type"))
			s.mu.Lock()
			if s.RequestContentTypes == nil {
				s.RequestContentTypes = map[string]int{}
			}
			s.RequestContentTypes[r.Method+" "+r.URL.Path+" "+ct]++
			s.mu.Unlock()
			bodyBytes := 0
			if r.ContentLength > 0 {
				bodyBytes = int(r.ContentLength)
			}
			ev := s.events.Add(Event{
				Host:        string(s.Host),
				Reason:      reasonNotLLMPath,
				RequestPath: r.URL.Path,
				Method:      r.Method,
				ContentType: ct,
				BodyBytes:   bodyBytes,
			})
			ctx := context.WithValue(r.Context(), eventSeqKey{}, ev.Seq)
			ctx = context.WithValue(ctx, reqStartKey{}, time.Now())
			r = r.WithContext(ctx)
		}
		proxy.ServeHTTP(w, r)
	})
	return mux
}

type eventSeqKey struct{}
type reqStartKey struct{}

func (s *Server) writeDirect(w http.ResponseWriter, r *http.Request, stats RewriteStats) {
	id := newToolCallID()
	if stats.Stream || wantsSSE(r) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(directChatSSE(id, stats.DirectName, stats.DirectArgs, stats.SentModel))
		return
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(directChatJSON(id, stats.DirectName, stats.DirectArgs, stats.SentModel))
}

func savedTokens(request []byte, stats RewriteStats, direct bool) *SavedTokens {
	saved := &SavedTokens{}
	if direct {
		saved.DirectInput = estimateTokens(string(request))
	}
	if stats.CompactApplied && stats.CharsBefore > stats.CharsAfter {
		saved.CompactionInput = estimateTokens(strings.Repeat("x", stats.CharsBefore-stats.CharsAfter))
	}
	if saved.DirectInput == 0 && saved.CompactionInput == 0 {
		return nil
	}
	return saved
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

func wantsSSE(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("accept")), "text/event-stream")
}

// connectJevAttemptHook records Jev calls made while Connect frames stream.
// The JSON path collects attempts before Add; Connect rewrites lazily inside
// the body copy goroutine, so attempts append to the existing event.
func (s *Server) connectJevAttemptHook(seq int64) func(jev.Attempt) {
	return func(a jev.Attempt) {
		ja := JevAttempt{
			Purpose: a.Purpose, Ms: a.Duration.Seconds() * 1000,
			OK: a.OK, Cached: a.Cached, ErrKind: a.ErrKind, Status: a.Status, Questions: a.Questions,
			InputTokens: usageInputTokens(a.Usage), OutputTokens: usageOutputTokens(a.Usage),
		}
		s.mu.Lock()
		if a.Cached {
			s.JevCacheHits++
		} else {
			s.JevHTTP++
			if a.OK {
				s.JevOK++
			} else {
				s.JevFail++
			}
		}
		s.mu.Unlock()
		s.events.Update(seq, func(e *Event) {
			e.JevAttempts = append(e.JevAttempts, ja)
			if a.Cached {
				e.JevCached++
				return
			}
			e.JevCalls++
			if a.Purpose == "selection" {
				e.SelectionJevCalls++
			} else {
				e.OtherJevCalls++
			}
			if !a.OK {
				e.JevFailed++
			}
		})
	}
}

const reasonNotLLMPath = "not_llm_path"
const reasonStream = "stream"

func looksDevinInference(path string) bool {
	return strings.HasSuffix(path, "/exa.api_server_pb.ApiServerService/GetChatMessage") ||
		strings.HasSuffix(path, "/exa.api_server_pb.ApiServerService/GetDevstralStream")
}

func looksLikeLLM(path string) bool {
	p := strings.ToLower(path)
	// Why: CountTokens is a control request with no usage block, not a model completion.
	if strings.HasSuffix(p, "/messages/count_tokens") {
		return false
	}
	// Grok session signals and turn deltas contain no model usage.
	if strings.Contains(p, "/sessions/") && (strings.HasSuffix(p, "/signals") || strings.HasSuffix(p, "/turn-deltas")) {
		return false
	}
	return strings.Contains(p, "/messages") ||
		strings.Contains(p, "/chat/completions") ||
		strings.Contains(p, "/responses") ||
		strings.Contains(p, "/sessions") ||
		strings.Contains(p, "/inference") ||
		strings.Contains(p, "/complete")
}

func (s *Server) autoApply(body []byte) string {
	return s.autoApplyContext(context.Background(), body)
}

func (s *Server) autoApplyContext(ctx context.Context, body []byte) string {
	if s == nil || !s.Options.AutoApply {
		return ""
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.LastDelivered = ""
	if s.Executor == nil {
		s.Executor = &recordingExec{}
	}
	if s.Apps == nil {
		s.Apps = NewAppStore()
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return ""
	}
	if tools, _ := extractTools(root); len(tools) > 0 {
		s.catalogMu.Lock()
		s.Catalog.Merge(plan.CapabilitiesFromSpecs(specsFromToolArray(tools), s.Host))
		s.catalogMu.Unlock()
	}
	cat, bodies := s.catalogSnapshot()
	if len(cat.Items) == 0 {
		return ""
	}
	msgs, _ := locateHistory(root)
	followup := false
	if len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			last, _ := msgs[i].(map[string]any)
			if last == nil || last["role"] == "system" {
				continue
			}
			if last["role"] != "user" || toolResultKey(last) != "" {
				followup = true
			}
			break
		}
	}
	_, user := itemsFromMessages(msgs)
	if user == "" {
		user = fallbackUser(root)
	}
	user = plan.WorkRequest(user)
	if strings.TrimSpace(user) == "" {
		return ""
	}
	s.ensureSkillBodies()
	cat, bodies = s.catalogSnapshot()
	var ask plan.ChoiceAsker
	if s.Client != nil && s.Client.Live() {
		ask = func(text string, criteria map[string]string) (string, float64, error) {
			qs := map[string]jev.Question{"capability": {Type: "choice", Instructions: "pick one capability id", Criteria: criteria}}
			res, err := s.Client.AskSelectionContext(ctx, map[string]any{"request": text}, qs)
			if err != nil {
				return "", 0, err
			}
			allowed := map[string]bool{}
			for k := range criteria {
				allowed[k] = true
			}
			ch, reason := jev.ValidateChoice(res, "capability", allowed)
			if reason != "" {
				return "", 0, errors.New(reason)
			}
			return ch.Choice, ch.Conf, nil
		}
	}
	var explicit []string
	low := strings.ToLower(user)
	var skills []plan.Capability
	for _, item := range cat.Items {
		if item.Kind == plan.KindSkill {
			skills = append(skills, item)
			if s.Options.KindMode(item.Kind) == KindApply && strings.Contains(low, strings.ToLower(item.Name)) {
				explicit = append(explicit, item.ID)
			}
		}
	}
	if len(explicit) == 0 {
		for _, item := range cat.Items {
			if item.Kind != plan.KindSkill && item.Provider != "host" && s.Options.KindMode(item.Kind) == KindApply && strings.Contains(low, strings.ToLower(item.Name)) && (item.Kind == plan.KindCLI || item.Kind == plan.KindMCP || item.Explicit) {
				explicit = append(explicit, item.ID)
			}
		}
	}
	filterSkills := len(skills) > 0 && len(explicit) == 0
	if len(explicit) > 0 {
		item, ok := plan.Lookup(cat, explicit[0])
		filterSkills = ok && item.Kind == plan.KindSkill
	}
	if filterSkills {
		cat.Items = skills
		cat.Revision = cat.ComputeRevision()
	}
	if app := s.Apps.Get(plan.DecisionIDFor(plan.RouteRequest{Text: user, Host: s.Host, Catalog: cat, NewRequest: true})); app != nil && app.State == AppDelivered && app.Kind == plan.KindSkill {
		if item, ok := plan.Lookup(cat, app.CapabilityID); ok && bodies[item.Target.SkillBodyRef] != "" {
			s.LastDelivered = "source: " + item.Target.SkillBodyRef + "\n" + bodies[item.Target.SkillBodyRef]
			return s.LastDelivered
		}
	}
	if followup {
		return ""
	}
	if len(explicit) == 0 {
		if len(skills) == 0 {
			return ""
		}
	}
	route := plan.Route(plan.RouteRequest{Text: user, Host: s.Host, Catalog: cat, Explicit: explicit, NewRequest: true}, ask)
	if route.Outcome != plan.RouteSelected {
		if s.Options.ApplicationPolicy == PolicyRequired && len(explicit) > 0 {
			s.ApplyErr = "required application: no selection (" + route.ReasonCode + ")"
		}
		return ""
	}
	item, ok := plan.Lookup(cat, route.CapabilityID)
	if !ok {
		if s.Options.ApplicationPolicy == PolicyRequired {
			s.ApplyErr = "required application: unknown capability"
		}
		return ""
	}
	mode := s.Options.KindMode(item.Kind)
	if mode == KindOff || mode == KindFixed || mode == KindObserve {
		return ""
	}
	if item.Kind != plan.KindSkill {
		named := false
		for _, id := range explicit {
			if id == item.ID {
				named = true
				break
			}
		}
		if !named {
			return ""
		}
		if reason := inspectBlocksCalls(body); reason != "" {
			if s.Options.ApplicationPolicy == PolicyRequired {
				s.ApplyErr = "required application: unsupported " + reason
			}
			return ""
		}
		return ""
	}
	app, err := Apply(s.Apps, route, cat, bodies, nil, s.Executor)
	if err != nil || !Success(app) && s.Options.ApplicationPolicy == PolicyRequired {
		if err != nil {
			s.ApplyErr = err.Error()
		} else if app != nil && app.State != AppStarted && app.State != AppDelivered {
			s.ApplyErr = "required application: not delivered"
		}
	}
	s.stampApp(app)
	if app != nil && app.Kind == plan.KindSkill && app.DeliveredHash != "" {
		if exec, ok := s.Executor.(*recordingExec); ok {
			exec.mu.Lock()
			if n := len(exec.skills); n > 0 {
				s.LastDelivered = exec.skills[n-1]
			}
			exec.mu.Unlock()
		}
	}
	return s.LastDelivered
}

func (s *Server) ensureSkillBodies() {
	if s == nil {
		return
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.ensureSkillBodiesLocked()
}

func (s *Server) ensureSkillBodiesLocked() {
	if s.SkillBodies == nil {
		s.SkillBodies = map[string]string{}
	}
	for _, item := range s.Catalog.Items {
		if item.Kind != plan.KindSkill {
			continue
		}
		ref := item.Target.SkillBodyRef
		if ref == "" || s.SkillBodies[ref] != "" {
			continue
		}
		if body := loadSkillBody(ref, item.Description); body != "" {
			s.SkillBodies[ref] = body
		}
	}
}

func (s *Server) catalogSnapshot() (plan.Catalog, map[string]string) {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	cat := s.Catalog
	// Why: Use copies instead of holding catalogMu through Apply, which can invoke a HostExecutor.
	cat.Items = append([]plan.Capability(nil), s.Catalog.Items...)
	bodies := make(map[string]string, len(s.SkillBodies))
	for ref, body := range s.SkillBodies {
		bodies[ref] = body
	}
	return cat, bodies
}

func loadSkillBody(ref, desc string) string {
	if ref != "" && !strings.Contains(ref, "..") && !strings.HasPrefix(ref, "host-skill:") && !strings.HasPrefix(ref, "skill://") {
		if raw, err := os.ReadFile(ref); err == nil && len(raw) > 0 {
			return string(raw)
		}
	}
	if dir := strings.TrimSpace(os.Getenv("JEV_SKILL_DIR")); dir != "" && ref != "" {
		name := strings.TrimPrefix(ref, "host-skill:")
		name = strings.TrimPrefix(name, "skill://")
		name = strings.TrimSuffix(name, "/SKILL.md")
		for _, p := range []string{filepath.Join(dir, name, "SKILL.md"), filepath.Join(dir, name+".md")} {
			if raw, err := os.ReadFile(p); err == nil && len(raw) > 0 {
				return string(raw)
			}
		}
	}
	return desc
}

type bodyObserver struct {
	rc   io.ReadCloser
	buf  []byte
	fn   func([]byte)
	once sync.Once
}

func wrapBodyObserve(rc io.ReadCloser, fn func([]byte)) io.ReadCloser {
	if rc == nil || fn == nil {
		return rc
	}
	return &bodyObserver{rc: rc, fn: fn}
}

func (w *bodyObserver) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if n > 0 && len(w.buf) < usageParseLimit {
		end := n
		if len(w.buf)+end > usageParseLimit {
			end = usageParseLimit - len(w.buf)
		}
		if end > 0 {
			w.buf = append(w.buf, p[:end]...)
		}
	}
	if err == io.EOF {
		w.flush()
	}
	return n, err
}

func (w *bodyObserver) Close() error {
	w.flush()
	return w.rc.Close()
}

func (w *bodyObserver) flush() {
	w.once.Do(func() {
		if w.fn != nil && len(w.buf) > 0 {
			w.fn(w.buf)
		}
	})
}

func (s *Server) observeResponse(raw []byte) {
	if s == nil {
		return
	}
	if json.Valid(raw) {
		s.observeResponseJSON(raw)
		return
	}
	if evs := sseDataPayloads(raw); len(evs) > 0 {
		for _, ev := range evs {
			s.observeResponseJSON(ev)
		}
		return
	}
	s.observeConnectStream(raw)
}

func (s *Server) observeConnectStream(raw []byte) {
	i := 0
	for i+5 <= len(raw) {
		ln := int(raw[i+1])<<24 | int(raw[i+2])<<16 | int(raw[i+3])<<8 | int(raw[i+4])
		if ln < 0 || i+5+ln > len(raw) {
			break
		}
		frame := raw[i : i+5+ln]
		s.observeHostFrames(frame)
		s.observeProtoJSON(connectFramePayload(frame))
		i += 5 + ln
	}
	s.observeProtoJSON(connectFramePayload(raw))
	s.observeProtoJSON(raw)
}

func (s *Server) observeProtoJSON(raw []byte) {
	if s == nil || len(raw) == 0 {
		return
	}
	for _, pj := range collectProtoJSON(raw) {
		if pj.obj == nil {
			continue
		}
		enc := []byte(pj.raw)
		s.observeJSONCalls(enc)
		s.observeCallObject(pj.obj)
		if item, ok := pj.obj["item"].(map[string]any); ok {
			s.observeCallObject(item)
		}
		for _, out := range asSlice(pj.obj["output"]) {
			obj, _ := out.(map[string]any)
			if obj != nil {
				s.observeCallObject(obj)
			}
		}
		if s.Apps != nil {
			observeJSONResults(s.Apps, enc)
		}
	}
}

func (s *Server) observeResponseJSON(raw []byte) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if msg := choiceMessage(root); msg != nil {
		enc, _ := json.Marshal(map[string]any{"messages": []any{msg}})
		s.observeJSONCalls(enc)
	}
	if asSlice(root["content"]) != nil {
		enc, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": root["content"]}}})
		s.observeJSONCalls(enc)
	}
	if cb, ok := root["content_block"].(map[string]any); ok {
		enc, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{cb}}}})
		s.observeJSONCalls(enc)
		s.observeCallObject(cb)
	}
	s.observeCallObject(root)
	if item, ok := root["item"].(map[string]any); ok {
		s.observeCallObject(item)
	}
	for _, out := range asSlice(root["output"]) {
		obj, _ := out.(map[string]any)
		if obj != nil {
			s.observeCallObject(obj)
		}
	}
	s.observeJSONCalls(raw)
}

func (s *Server) observeCallObject(obj map[string]any) {
	if obj == nil {
		return
	}
	switch firstString(obj, "type") {
	case "function_call", "custom_tool_call", "local_shell_call", "tool_use":
		s.startObservedCall(firstString(obj, "call_id", "id"), firstString(obj, "name"))
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		if s.Apps == nil {
			return
		}
		id := firstString(obj, "call_id", "id")
		text := resultText(obj["output"])
		if text == "" {
			text = resultText(obj["result"])
		}
		if id != "" && text != "" {
			_ = ObserveHostCall(s.Apps, id, text, 0)
		}
	}
}

func choiceMessage(root map[string]any) map[string]any {
	for _, ch := range asSlice(root["choices"]) {
		m, _ := ch.(map[string]any)
		if m == nil {
			continue
		}
		if msg, ok := m["message"].(map[string]any); ok {
			return msg
		}
		if delta, ok := m["delta"].(map[string]any); ok {
			return delta
		}
	}
	return nil
}

func resultText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, p := range t {
			if m, ok := p.(map[string]any); ok {
				if text, _ := m["text"].(string); text != "" {
					b.WriteString(text)
					continue
				}
				if text, _ := m["content"].(string); text != "" {
					b.WriteString(text)
				}
				continue
			}
			if text, ok := p.(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}

func sseDataPayloads(raw []byte) [][]byte {
	var out [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if json.Valid(data) {
			out = append(out, data)
		}
	}
	return out
}

func (s *Server) observeLastUserResult(raw []byte) {
	if s == nil || s.Apps == nil || !json.Valid(raw) {
		return
	}
	observeJSONResults(s.Apps, raw)
}

func (s *Server) observeHostFrames(frame []byte) {
	if s == nil || s.Apps == nil {
		return
	}
	payload := frame
	if looksConnectFrame(frame) {
		payload = connectFramePayload(frame)
	} else if len(frame) >= 5 {
		ln := int(frame[1])<<24 | int(frame[2])<<16 | int(frame[3])<<8 | int(frame[4])
		if ln+5 == len(frame) {
			payload = frame[5:]
		}
	}
	if lift, ok := liftDevinNativeProto(payload); ok {
		s.observeLiftedHostTurns(lift.root)
	} else {
		// A lifted request includes the whole tool catalog. Scanning its leaf
		// strings would mistake a catalog entry for an executed call.
		s.observeProtoExecLeaves(payload)
	}
	if inner := firstLD(payload, 1); json.Valid(inner) {
		s.observeJSONCalls(inner)
		observeJSONResults(s.Apps, inner)
		return
	}
	if json.Valid(payload) {
		s.observeJSONCalls(payload)
		observeJSONResults(s.Apps, payload)
	}
}

func observedExecName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || name != strings.TrimSpace(name) || strings.ContainsAny(n, " \n\t") {
		return false
	}
	if n == "get_output" || n == "exec" || n == "exec_command" || n == "bash" || n == "rg" {
		return true
	}
	return strings.Contains(n, "shell") || strings.Contains(n, "terminal")
}

func observedSubagentName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || name != strings.TrimSpace(name) || strings.ContainsAny(n, " \n\t") {
		return false
	}
	return n == "agent" || n == "task" || n == "spawn_agent" || n == "spawn_subagent" || n == "run_subagent" || strings.Contains(n, "subagent")
}

func (s *Server) observeLiftedHostTurns(root map[string]any) {
	if s == nil || s.Apps == nil || root == nil {
		return
	}
	msgs, _ := locateHistory(root)
	known := map[string]bool{}
	for _, raw := range asSlice(root["tools"]) {
		if tool, ok := raw.(map[string]any); ok {
			known[strings.ToLower(toolNameOf(tool))] = true
		}
	}
	for _, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil || len(asSlice(m["tool_calls"])) == 0 {
			continue
		}
		// The native lift also sees provider and call IDs. Only catalog names
		// are tool calls; each accepted call/result keeps its synthetic ID.
		accepted := map[string]bool{}
		filtered := make([]any, 0, len(msgs))
		for _, raw := range msgs {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			for _, tc := range asSlice(item["tool_calls"]) {
				call, _ := tc.(map[string]any)
				fn, _ := call["function"].(map[string]any)
				name := firstString(fn, "name")
				id := firstString(call, "id", "call_id")
				if id != "" && known[strings.ToLower(name)] {
					accepted[id] = true
					filtered = append(filtered, map[string]any{"tool_calls": []any{call}})
				}
			}
			if id := firstString(item, "tool_call_id"); accepted[id] {
				filtered = append(filtered, item)
			}
		}
		if raw, err := json.Marshal(map[string]any{"messages": filtered}); err == nil {
			s.observeJSONCalls(raw)
			observeJSONResults(s.Apps, raw)
		}
		return
	}
	var lastID, lastName, lastResult string
	for _, m := range msgs {
		obj, _ := m.(map[string]any)
		if obj == nil {
			continue
		}
		if firstString(obj, "type") == "function_call" {
			name := firstString(obj, "name")
			if observedExecName(name) {
				lastID, lastName = firstString(obj, "call_id", "id"), name
			}
		}
		for _, tc := range asSlice(obj["tool_calls"]) {
			tcm, _ := tc.(map[string]any)
			if tcm == nil {
				continue
			}
			name := firstString(tcm, "name")
			if fn, ok := tcm["function"].(map[string]any); ok && name == "" {
				name = firstString(fn, "name")
			}
			if observedExecName(name) {
				lastID, lastName = firstString(tcm, "id", "call_id"), name
			}
		}
		parts, _ := obj["content"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			switch firstString(pm, "type") {
			case "tool_use":
				name := firstString(pm, "name")
				if observedExecName(name) {
					lastID, lastName = firstString(pm, "id"), name
				}
			case "tool_result":
				if text := resultText(pm["content"]); text != "" {
					if id := firstString(pm, "tool_use_id", "tool_call_id"); id != "" {
						lastID = id
					}
					lastResult = text
				}
			}
		}
		if id := firstString(obj, "tool_call_id"); id != "" {
			if text := resultText(obj["content"]); text != "" {
				lastID, lastResult = id, text
				if name := firstString(obj, "name"); observedExecName(name) {
					lastName = name
				}
			}
		}
	}
	if lastResult == "" {
		for _, m := range msgs {
			obj, _ := m.(map[string]any)
			if obj == nil {
				continue
			}
			if text := resultText(obj["content"]); looksExecResult(text) {
				lastResult = text
				if lastName == "" {
					lastName = "exec"
				}
			}
		}
	}
	if lastName != "" {
		s.startObservedCall(lastID, lastName)
	}
	if lastResult != "" {
		if err := ObserveHostCall(s.Apps, lastID, lastResult, 0); err != nil {
			_ = ObserveHostCall(s.Apps, "", lastResult, 0)
		}
	}
}

func looksExecResult(s string) bool {
	return strings.Contains(s, "Exit code:") || strings.Contains(s, "Output from command")
}

func looksCallID(s string) bool {
	if len(s) < 8 || len(s) > 80 {
		return false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F', c == '-':
			n++
		default:
			return false
		}
	}
	return strings.Count(s, "-") >= 1
}

func (s *Server) observeProtoExecLeaves(raw []byte) {
	if s == nil || s.Apps == nil {
		return
	}
	fields, ok := parseProtoFields(raw)
	if !ok {
		return
	}
	var name, id, result string
	for _, field := range fields {
		if field.wire != 2 || !protoLikelyLeafText(field.raw) {
			continue
		}
		text := string(field.raw)
		switch field.field {
		case 1:
			if observedExecName(text) || observedSubagentName(text) {
				name = text
			} else if looksExecResult(text) {
				result = text
			}
		case 12:
			if looksCallID(text) {
				id = text
			}
		}
	}
	if name != "" && id != "" {
		s.startObservedCall(id, name)
	}
	if result != "" {
		// Legacy native exec frames omit the result ID; AppStore accepts it
		// only when exactly one call is pending.
		_ = ObserveHostCall(s.Apps, id, result, 0)
	}
}

func (s *Server) observeJSONCalls(raw []byte) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	msgs, _ := locateHistory(root)
	for _, m := range msgs {
		obj, _ := m.(map[string]any)
		if obj == nil {
			continue
		}
		if firstString(obj, "type") == "function_call" {
			s.startObservedCall(firstString(obj, "call_id"), firstString(obj, "name"))
		}
		for _, tc := range asSlice(obj["tool_calls"]) {
			tcm, _ := tc.(map[string]any)
			if tcm == nil {
				continue
			}
			id, _ := tcm["id"].(string)
			name := firstString(tcm, "name")
			if fn, ok := tcm["function"].(map[string]any); ok && name == "" {
				name = firstString(fn, "name")
			}
			s.startObservedCall(id, name)
		}
		parts, _ := obj["content"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			if firstString(pm, "type") != "tool_use" {
				continue
			}
			s.startObservedCall(firstString(pm, "id"), firstString(pm, "name"))
		}
	}
}

func (s *Server) startObservedCall(callID, name string) {
	if s == nil || s.Apps == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if plan.HostMeta(name) {
		return
	}
	if s.Executor == nil {
		s.Executor = &recordingExec{}
	}
	for _, app := range s.Apps.Snapshot() {
		if app.CallID == callID || (app.State == AppStarted && strings.EqualFold(app.Kind, plan.KindCLI) && name == "exec") {
			return
		}
	}
	var item plan.Capability
	found := false
	s.catalogMu.Lock()
	for _, cand := range s.Catalog.Items {
		if strings.EqualFold(cand.Name, name) && cand.Kind != plan.KindSkill {
			item, found = cand, true
			break
		}
	}
	if !found {
		kind := plan.KindMCP
		if observedExecName(name) {
			kind = plan.KindCLI
		} else if observedSubagentName(name) {
			kind = plan.KindSubagent
		}
		item = plan.Capability{
			ID: plan.CapabilityID(kind, "host", name, "obs"), Kind: kind, Name: name,
			Provider: "host", Version: "obs", Availability: plan.AvailInstalled,
		}
		if kind == plan.KindCLI {
			item.Target = plan.Target{CLICommand: name}
		} else if kind == plan.KindSubagent {
			item.Target = plan.Target{SubagentLauncher: name}
		} else {
			item.Target = plan.Target{MCPConn: "host-catalog", MCPTool: name}
		}
		s.Catalog.Merge([]plan.Capability{item})
	}
	cat, bodies := s.catalogSnapshotLocked()
	s.catalogMu.Unlock()
	route := plan.RouteResult{DecisionID: callID, Outcome: plan.RouteSelected, CapabilityID: item.ID}
	if route.DecisionID == "" {
		route.DecisionID = item.ID
	}
	app, err := Apply(s.Apps, route, cat, bodies, nil, s.Executor)
	if err != nil || app == nil {
		return
	}
	if callID != "" && app.CallID != callID {
		app.CallID = callID
		s.Apps.put(app)
	}
	s.stampApp(app)
}

func (s *Server) catalogSnapshotLocked() (plan.Catalog, map[string]string) {
	cat := s.Catalog
	cat.Items = append([]plan.Capability(nil), s.Catalog.Items...)
	bodies := make(map[string]string, len(s.SkillBodies))
	for ref, body := range s.SkillBodies {
		bodies[ref] = body
	}
	return cat, bodies
}

func (s *Server) stampApp(app *Application) {
	if s == nil || app == nil || s.Apps == nil {
		return
	}
	if app.Host != "" {
		return
	}
	app.Host = string(s.Host)
	s.Apps.put(app)
}

func observeJSONResults(store *AppStore, raw []byte) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	msgs, _ := locateHistory(root)
	for _, m := range msgs {
		obj, _ := m.(map[string]any)
		if obj == nil {
			continue
		}
		if id, _ := obj["tool_call_id"].(string); id != "" {
			if text := resultText(obj["content"]); text != "" {
				_ = ObserveHostCall(store, id, text, 0)
			}
		}
		typ := firstString(obj, "type")
		if typ == "function_call_output" || typ == "custom_tool_call_output" || typ == "local_shell_call_output" {
			if id := firstString(obj, "call_id", "id"); id != "" {
				if text := resultText(obj["output"]); text != "" {
					_ = ObserveHostCall(store, id, text, 0)
				}
			}
		}
		parts, _ := obj["content"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			if firstString(pm, "type") != "tool_result" {
				continue
			}
			id, _ := pm["tool_use_id"].(string)
			if text := resultText(pm["content"]); text != "" {
				_ = ObserveHostCall(store, id, text, 0)
			}
		}
	}
}

func specsFromToolArray(tools []any) []plan.Spec {
	return plan.SpecsFrom(asMaps(tools))
}

func inspectBlocksCalls(body []byte) string {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return ""
	}
	el := inspectRequest(root)
	switch el.Reason {
	case reasonPreviousResponse, reasonExplicitToolChoice:
		return el.Reason
	default:
		return ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
