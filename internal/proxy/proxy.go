package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

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
	}
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
	return &Server{
		Listen:     listen,
		Host:       h,
		Upstream:   u,
		Client:     client,
		Log:        log.New(logWriter, "jev-routing ", log.LstdFlags),
		Options:    opt,
		events:     newEventLog(),
		publicBind: listenIsPublic(listen),
		listenPort: listenPortOf(listen),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
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
			raw, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
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
