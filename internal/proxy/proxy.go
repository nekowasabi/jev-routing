package proxy

import (
	"bytes"
	"encoding/json"
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
	Listen   string
	Host     host.ID
	Upstream *url.URL
	Client   *jev.Client
	Log      *log.Logger
	mu       sync.Mutex
	Last     RewriteStats
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
		return "https://api.openai.com"
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
		_ = json.NewEncoder(w).Encode(s.Last)
	})
	proxy := httputil.NewSingleHostReverseProxy(s.Upstream)
	orig := proxy.Director
	proxy.Director = func(r *http.Request) {
		orig(r)
		r.Host = s.Upstream.Host
		r.Header.Set("host", s.Upstream.Host)
		r.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = func(res *http.Response) error { return nil }
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && looksLikeLLM(r.URL.Path) {
			raw, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err == nil && json.Valid(raw) {
				rewritten, stats, rerr := Rewrite(raw, s.Host, s.Client)
				if rerr == nil {
					s.mu.Lock()
					s.Last = stats
					s.mu.Unlock()
					s.Log.Print(FormatStats(stats))
					raw = rewritten
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
	return strings.Contains(p, "/messages") ||
		strings.Contains(p, "/chat/completions") ||
		strings.Contains(p, "/responses")
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
