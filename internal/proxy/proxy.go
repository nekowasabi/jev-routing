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

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

type Server struct {
	Listen      string
	Host        host.ID
	Upstream    *url.URL
	Client      *jev.Client
	Log         *log.Logger
	mu          sync.Mutex
	Last        RewriteStats
	Requests    int
	CharsBefore int
	CharsAfter  int
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
		return "https://api.devin.ai"
	default:
		if u := os.Getenv("GROK_OAUTH_UPSTREAM"); u != "" {
			return u
		}
		return "https://cli-chat-proxy.grok.com"
	}
}

// New builds the proxy. logWriter receives the rewrite log; nil means os.Stderr.
// Why: `run` hands the terminal to a raw-mode child (Claude Code TUI), so async
// log lines must go to a file instead of the shared stderr fd.
func New(listen string, h host.ID, client *jev.Client, logWriter io.Writer) (*Server, error) {
	u, err := url.Parse(DefaultUpstream(h))
	if err != nil {
		return nil, err
	}
	if logWriter == nil {
		logWriter = os.Stderr
	}
	return &Server{
		Listen:   listen,
		Host:     h,
		Upstream: u,
		Client:   client,
		Log:      log.New(logWriter, "jev-routing ", log.LstdFlags),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RewriteStats
			Requests    int `json:"requests"`
			CharsBefore int `json:"charsBefore"`
			CharsAfter  int `json:"charsAfter"`
		}{s.Last, s.Requests, s.CharsBefore, s.CharsAfter})
	})
	proxy := httputil.NewSingleHostReverseProxy(s.Upstream)
	orig := proxy.Director
	proxy.Director = func(r *http.Request) {
		// Why: Codex sends custom-provider requests to /v1, while ChatGPT
		// authentication is accepted only by its /backend-api/codex endpoint.
		if s.Host == host.Codex && strings.HasPrefix(s.Upstream.Path, "/backend-api/codex") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1")
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/v1")
		}
		orig(r)
		r.Host = s.Upstream.Host
		r.Header.Set("host", s.Upstream.Host)
		r.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = func(res *http.Response) error { return nil }
	proxy.ErrorLog = s.Log
	// Why: Instead of ReverseProxy default ErrorHandler (prints to log.Default / TUI stderr), adopted custom handler. Reason: Grok TUI shares stderr; client cancel is expected noise.
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.Log.Printf("proxy error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && looksLikeLLM(r.URL.Path) {
			s.mu.Lock()
			s.Requests++
			s.mu.Unlock()
			raw, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err == nil && json.Valid(raw) {
				rewritten, stats, rerr := Rewrite(raw, s.Host, s.Client)
				if rerr == nil {
					s.mu.Lock()
					s.Last = stats
					s.CharsBefore += len(raw)
					s.mu.Unlock()
					s.Log.Print(FormatStats(stats))
					raw = rewritten
					s.mu.Lock()
					s.CharsAfter += len(raw)
					s.mu.Unlock()
				} else {
					s.Log.Printf("rewrite skipped: %v", rerr)
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			r.Header.Set("Content-Length", itoa(len(raw)))
		}
		proxy.ServeHTTP(w, r)
	})
	return mux
}

func looksLikeLLM(path string) bool {
	p := strings.ToLower(path)
	// Why: Instead of only OpenAI/Anthropic paths, also match Cursor /aiserver.
	// Reason: cursor-agent posts Connect RPCs under that prefix; JSON bodies still rewrite.
	return strings.Contains(p, "/messages") ||
		strings.Contains(p, "/chat/completions") ||
		strings.Contains(p, "/responses") ||
		strings.Contains(p, "/aiserver")
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
