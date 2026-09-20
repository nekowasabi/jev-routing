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

	cursorAgentHost string
	cursorTask      string

	Catalog       plan.Catalog
	SkillBodies   map[string]string
	Executor      HostExecutor
	Apps          *AppStore
	ApplyErr      string
	LastDelivered string
}

func (s *Server) setCursorTask(task string) {
	if s == nil || strings.TrimSpace(task) == "" {
		return
	}
	s.mu.Lock()
	if s.cursorTask == "" {
		s.cursorTask = task
	}
	s.mu.Unlock()
}

func (s *Server) cursorTaskCopy() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursorTask
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
	apps := withModelRouteApps(s.Apps.Snapshot())
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
		"lastDelivered":       s.LastDelivered,
		"applyErr":            s.ApplyErr,
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
	case host.Cursor:
		if u := os.Getenv("CURSOR_UPSTREAM"); u != "" {
			return u
		}
		return "https://api2.cursor.sh"
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
	s.Options.AfterRewrite = func(body []byte) []byte {
		s.autoApply(body)
		if s.LastDelivered == "" {
			return body
		}
		out, err := ApplyHostContext(s.Host, body, s.LastDelivered)
		if err != nil {
			if s.Options.ApplicationPolicy == PolicyRequired {
				s.ApplyErr = err.Error()
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

func (r *recordingExec) DeliverSkill(_ string, body, source string) error {
	r.mu.Lock()
	r.skills = append(r.skills, "source: "+source+"\n"+body)
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
		if s.rewriteStreamingAgentURL(r) {
			r.Header.Set("host", r.Host)
		} else {
			r.Host = s.Upstream.Host
			r.Header.Set("host", s.Upstream.Host)
		}
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
		ct := res.Header.Get("content-type")
		if strings.Contains(strings.ToLower(ct), "proto") {
			res.Body = wrapProtoHosts(res.Body, func(hosts []string) {
				if len(hosts) == 0 {
					return
				}
				s.rememberCursorAgentHost(hosts)
				s.events.Update(seq, func(e *Event) {
					e.URLHosts = mergeHosts(e.URLHosts, hosts)
				})
			})
		}
		res.Body = wrapBodyObserve(res.Body, func(raw []byte) {
			s.observeResponse(raw)
		})
		res.Body = wrapUsage(res.Body, ct, func(u *NormalizedUsage, partial bool, missing, finish string) {
			bodyMs := time.Since(started).Seconds() * 1000
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
		if r.Method == http.MethodPost && (looksStreamingAgent(r.URL.Path) || looksDevinInference(r.URL.Path)) {
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
			if looksStreamingAgent(r.URL.Path) && s.Host == host.Cursor && connectCursorContentType(ct) {
				r.Body = wrapConnectCursorBody(r.Body, s, ev.Seq, r.Context())
				r.ContentLength = -1
				r.Header.Del("Content-Length")
			} else if looksDevinInference(r.URL.Path) && connectCursorContentType(ct) {
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
			shape := catalogShape(raw)
			var urlHosts []string
			if !origJSON {
				urlHosts = protoURLHosts(raw)
				s.rememberCursorAgentHost(urlHosts)
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
			if err == nil && json.Valid(raw) {
				rewritten, st, rerr := RewriteWith(ctx, raw, s.Host, s.Client, s.Options)
				stats = st
				if rerr == nil {
					s.mu.Lock()
					s.Last = stats
					s.CharsBefore += len(raw)
					if stats.Changed {
						s.Rewritten++
					} else {
						s.Passthrough++
					}
					s.JevHTTP += jevHTTP
					s.JevOK += jevOK
					s.JevFail += jevFail
					s.JevCacheHits += jevCache
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
					s.autoApply(raw)
					s.observeLastUserResult(raw)
					if extra := s.LastDelivered; extra != "" {
						if withCtx, err := ApplyHostContext(s.Host, rewritten, extra); err == nil {
							rewritten = withCtx
						} else if s.Options.ApplicationPolicy == PolicyRequired {
							s.ApplyErr = err.Error()
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
			ev := Event{
				Host:               string(s.Host),
				Source:             stats.Source,
				Reason:             stats.Reason,
				Apply:              stats.Apply,
				Chosen:             stats.Chosen,
				Changed:            stats.Changed,
				OriginalModel:      stats.OriginalModel,
				SentModel:          stats.SentModel,
				ToolBefore:         stats.ToolBefore,
				ToolAfter:          stats.ToolAfter,
				ToolsBefore:        stats.ToolsBefore,
				ToolsAfter:         stats.ToolsAfter,
				HistoryTypes:       stats.HistoryTypes,
				UnsupportedHistory: stats.UnsupportedHistory,
				HistoryIssues:      stats.HistoryIssues,
				CompactDropped:     stats.CompactDropped,
				CompactApplied:     stats.CompactApplied,
				ReasoningChanged:   stats.ReasoningChanged,
				RequestPath:        r.URL.Path,
				Method:             r.Method,
				ContentType:        ct,
				BodyBytes:          origBytes,
				JsonValid:          &origJSON,
				URLHosts:           urlHosts,
				Catalog:            shape,
				JevAttempts:        attempts,
				JevCalls:           jevHTTP,
				SelectionJevCalls:  selectionJevCalls(attempts),
				OtherJevCalls:      jevHTTP - selectionJevCalls(attempts),
				JevCached:          jevCache,
				JevFailed:          jevFail,
				Protocol:           stats.Protocol,
			}
			if stats.Source != "" {
				c := stats.Confidence
				ev.Confidence = &c
			}
			if stats.NeedsTool != 0 || stats.Source == sourceJev {
				n := stats.NeedsTool
				ev.NeedsTool = &n
			}
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
			if !a.OK {
				e.JevFailed++
			}
		})
	}
}

const reasonNotLLMPath = "not_llm_path"
const reasonStream = "stream"

func (s *Server) rememberCursorAgentHost(hosts []string) {
	for _, h := range hosts {
		if !cursorAgentHost(h) {
			continue
		}
		s.mu.Lock()
		s.cursorAgentHost = h
		s.mu.Unlock()
		return
	}
}

func (s *Server) rewriteStreamingAgentURL(r *http.Request) bool {
	if s.Host != host.Cursor || !looksStreamingAgent(r.URL.Path) {
		return false
	}
	s.mu.Lock()
	h := s.cursorAgentHost
	s.mu.Unlock()
	if h == "" {
		return false
	}
	r.URL.Scheme = "https"
	r.URL.Host = h
	r.Host = h
	return true
}

func looksStreamingAgent(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "/agent.v1.agentservice/run") ||
		strings.Contains(p, "/bidiservice/") ||
		strings.Contains(p, "bidiappend")
}

func looksDevinInference(path string) bool {
	return strings.HasSuffix(path, "/exa.api_server_pb.ApiServerService/GetChatMessage") ||
		strings.HasSuffix(path, "/exa.api_server_pb.ApiServerService/GetDevstralStream")
}

func looksLikeLLM(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "/messages") ||
		strings.Contains(p, "/chat/completions") ||
		strings.Contains(p, "/responses") ||
		strings.Contains(p, "/aiserver") ||
		strings.Contains(p, "/agent.") ||
		strings.Contains(p, "/agent/") ||
		strings.Contains(p, "/sessions") ||
		strings.Contains(p, "/inference") ||
		strings.Contains(p, "/complete")
}

func (s *Server) autoApply(body []byte) {
	if s == nil || !s.Options.AutoApply {
		return
	}
	s.LastDelivered = ""
	if s.Executor == nil {
		s.Executor = &recordingExec{}
	}
	if s.Apps == nil {
		s.Apps = NewAppStore()
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return
	}
	if tools, _ := extractTools(root); len(tools) > 0 {
		s.Catalog.Merge(plan.CapabilitiesFromSpecs(specsFromToolArray(tools), s.Host))
	}
	if len(s.Catalog.Items) == 0 {
		return
	}
	msgs, _ := locateHistory(root)
	_, user := itemsFromMessages(msgs)
	if user == "" {
		user = fallbackUser(root)
	}
	user = plan.WorkRequest(user)
	if strings.TrimSpace(user) == "" {
		return
	}
	s.ensureSkillBodies()
	var ask plan.ChoiceAsker
	if s.Client != nil && s.Client.Live() {
		ask = func(text string, criteria map[string]string) (string, float64, error) {
			qs := map[string]jev.Question{"capability": {Type: "choice", Instructions: "pick one capability id", Criteria: criteria}}
			res, err := s.Client.AskSelectionContext(nil, map[string]any{"request": text}, qs)
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
	for _, item := range s.Catalog.Items {
		if strings.Contains(low, strings.ToLower(item.Name)) && (item.Kind == plan.KindSkill || item.Kind == plan.KindCLI || item.Kind == plan.KindMCP || item.Explicit) {
			explicit = append(explicit, item.ID)
		}
	}
	route := plan.Route(plan.RouteRequest{Text: user, Host: s.Host, Catalog: s.Catalog, Explicit: explicit, NewRequest: true}, ask)
	if route.Outcome != plan.RouteSelected {
		if s.Options.ApplicationPolicy == PolicyRequired && len(explicit) > 0 {
			s.ApplyErr = "required application: no selection (" + route.ReasonCode + ")"
		}
		return
	}
	item, ok := plan.Lookup(s.Catalog, route.CapabilityID)
	if !ok {
		if s.Options.ApplicationPolicy == PolicyRequired {
			s.ApplyErr = "required application: unknown capability"
		}
		return
	}
	mode := s.Options.KindMode(item.Kind)
	if mode == KindOff || mode == KindFixed || mode == KindObserve {
		return
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
			return
		}
		if reason := inspectBlocksCalls(body); reason != "" {
			if s.Options.ApplicationPolicy == PolicyRequired {
				s.ApplyErr = "required application: unsupported " + reason
			}
			return
		}
		return
	}
	app, err := Apply(s.Apps, route, s.Catalog, s.SkillBodies, nil, s.Executor)
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
}

func (s *Server) ensureSkillBodies() {
	if s == nil {
		return
	}
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

func collectProtoLeafTexts(body []byte, depth int) []string {
	if depth > 6 || len(body) == 0 {
		return nil
	}
	if protoLikelyLeafText(body) {
		return []string{string(body)}
	}
	fields, ok := parseProtoFields(body)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range fields {
		if f.wire != 2 {
			continue
		}
		out = append(out, collectProtoLeafTexts(f.raw, depth+1)...)
	}
	return out
}

func (s *Server) observeHostFrames(frame []byte) {
	if s == nil || s.Apps == nil {
		return
	}
	if id, result := cursorExecResult(frame); result != "" {
		s.startObservedCall(id, "exec")
		_ = ObserveHostCall(s.Apps, id, result, 0)
		return
	}
	payload := frame
	if looksConnectFrame(frame) {
		payload = connectFramePayload(frame)
		if id, result := cursorExecResult(connectFrame(0, payload)); result != "" {
			s.startObservedCall(id, "exec")
			_ = ObserveHostCall(s.Apps, id, result, 0)
			return
		}
	} else if len(frame) >= 5 {
		ln := int(frame[1])<<24 | int(frame[2])<<16 | int(frame[3])<<8 | int(frame[4])
		if ln+5 == len(frame) {
			payload = frame[5:]
		}
	}
	if req, _, ok := unwrapAgentRun(payload); ok {
		if lift, ok := liftCursorAgent(req); ok {
			s.observeLiftedHostTurns(lift.root)
		}
	} else if lift, ok := liftCursorAgent(payload); ok {
		s.observeLiftedHostTurns(lift.root)
	}
	if lift, ok := liftDevinNativeProto(payload); ok {
		s.observeLiftedHostTurns(lift.root)
	}
	s.observeProtoExecLeaves(payload)
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
	if n == "" || strings.ContainsAny(n, " \n\t") {
		return false
	}
	if n == "get_output" || n == "exec" || n == "exec_command" || n == "bash" || n == "rg" {
		return true
	}
	return strings.Contains(n, "shell") || strings.Contains(n, "terminal")
}

func observedSubagentName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || strings.ContainsAny(n, " \n\t") {
		return false
	}
	return n == "agent" || n == "task" || n == "spawn_agent" || n == "spawn_subagent" || n == "run_subagent" || strings.Contains(n, "subagent")
}

func (s *Server) observeLiftedHostTurns(root map[string]any) {
	if s == nil || s.Apps == nil || root == nil {
		return
	}
	msgs, _ := locateHistory(root)
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
	leaves := collectProtoLeafTexts(raw, 0)
	if t := firstLD(raw, 1); protoLikelyText(t) {
		leaves = append(leaves, string(t))
	}
	if protoLikelyText(raw) {
		leaves = append(leaves, string(raw))
	}
	if len(leaves) == 0 {
		return
	}
	var name, id, result string
	hits := 0
	seen := map[string]bool{}
	for _, t := range leaves {
		if observedExecName(t) || observedSubagentName(t) {
			key := strings.ToLower(strings.TrimSpace(t))
			if !seen[key] {
				seen[key] = true
				hits++
				name = t
			}
			continue
		}
		if looksCallID(t) {
			id = t
			continue
		}
		if looksExecResult(t) {
			result = t
		}
	}
	if hits == 1 && name != "" {
		s.startObservedCall(id, name)
	}
	if result != "" {
		if err := ObserveHostCall(s.Apps, id, result, 0); err != nil {
			_ = ObserveHostCall(s.Apps, "", result, 0)
		}
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
	route := plan.RouteResult{DecisionID: callID, Outcome: plan.RouteSelected, CapabilityID: item.ID}
	if route.DecisionID == "" {
		route.DecisionID = item.ID
	}
	app, err := Apply(s.Apps, route, s.Catalog, s.SkillBodies, nil, s.Executor)
	if err != nil || app == nil {
		return
	}
	if callID != "" && app.CallID != callID {
		app.CallID = callID
		s.Apps.put(app)
	}
	s.stampApp(app)
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

func cursorExecResult(raw []byte) (id, result string) {
	body := raw
	if looksConnectFrame(raw) {
		body = connectFramePayload(raw)
	} else if len(raw) >= 5 {
		ln := int(raw[1])<<24 | int(raw[2])<<16 | int(raw[3])<<8 | int(raw[4])
		if ln+5 == len(raw) {
			body = raw[5:]
		}
	}
	exec := firstLD(body, 2)
	if exec == nil || looksWrappedAgentRun(exec) {
		return "", ""
	}
	id = strings.TrimSpace(string(firstLD(exec, 1)))
	if r := firstLD(exec, 7); len(r) > 0 {
		if inner := firstLD(r, 1); len(inner) > 0 && protoLikelyText(inner) {
			return id, string(inner)
		}
		if protoLikelyText(r) {
			return id, string(r)
		}
	}
	execFields, ok := parseProtoFields(exec)
	if !ok {
		return id, ""
	}
	var best []byte
	for _, f := range execFields {
		if f.wire != 2 || f.field == 1 || f.field == 10 {
			continue
		}
		_, text := longestProtoLikelyText(f.raw, []int{f.field})
		if len(text) == 0 || !protoLikelyText(text) {
			continue
		}
		if bytes.Contains(text, []byte(`"properties"`)) || bytes.Contains(text, []byte(`"type":"object"`)) {
			continue
		}
		if len(text) > len(best) {
			best = text
		}
	}
	if len(best) == 0 {
		return id, ""
	}
	return id, string(best)
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
