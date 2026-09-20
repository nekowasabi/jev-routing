package proxy

import (
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/dashboard.html assets/dashboard.js assets/jev-gateway-LICENSE.txt
var dashboardFS embed.FS

var dashboardHTML = mustRead("assets/dashboard.html")

func mustRead(name string) []byte {
	b, err := dashboardFS.ReadFile(name)
	if err != nil {
		return []byte("dashboard missing")
	}
	return b
}

func nowUTC() time.Time { return time.Now().UTC() }

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAllowed(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/assets/dashboard.js") {
		w.Header().Set("content-type", "text/javascript; charset=utf-8")
		w.Header().Set("cache-control", "no-store")
		b, _ := dashboardFS.ReadFile("assets/dashboard.js")
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(dashboardHTML)
}

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	if !s.dashboardAllowed(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	since := int64(0)
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "invalid since", http.StatusBadRequest)
			return
		}
		since = n
	}
	events, recorded, oldest, truncated := s.events.Snapshot(since)
	all, _, _, _ := s.events.Snapshot(0)
	s.mu.Lock()
	counts := s.events.Counts()
	s.mu.Unlock()
	apps := withModelRouteApps(s.Apps.Snapshot())
	metrics := MergeMetrics(MetricsFromEvents(all), MetricsFromApps(apps))
	metrics.ByClass = ClassMap(apps, all, s.Options, string(s.Host))
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"router": map[string]any{
			"instanceId": s.events.InstanceID,
			"startedAt":  s.events.StartedAt,
			"now":        nowUTC(),
			"recorded":   recorded,
			"oldestSeq":  oldest,
			"counts":     counts,
			"mode":       s.Options.Mode,
			"runId":      s.Options.RunID,
			"kindModes":  copyKindModes(s.Options.KindModes),
			"reasoning":  s.Options.Reasoning,
			"compaction": s.Options.Compaction,
		},
		"events":           events,
		"metrics":          metrics,
		"applications":     publicApplications(apps),
		"historyTruncated": truncated,
	})
}

func (s *Server) dashboardAllowed(r *http.Request) bool {
	if s.publicBind {
		return false
	}
	if !loopbackAddr(r.RemoteAddr) {
		return false
	}
	if !loopbackHost(r.Host, s.listenPort) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
		return false
	}
	return true
}

func loopbackAddr(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackHost(hostport, listenPort string) bool {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
		p = ""
	}
	if listenPort != "" && p != "" && p != listenPort {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(origin, host string) bool {
	origin = strings.TrimSpace(origin)
	if strings.HasPrefix(origin, "http://") {
		origin = strings.TrimPrefix(origin, "http://")
	} else if strings.HasPrefix(origin, "https://") {
		origin = strings.TrimPrefix(origin, "https://")
	} else {
		return false
	}
	return strings.EqualFold(origin, host)
}

func listenIsPublic(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if h == "localhost" {
		return false
	}
	if h == "" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return h != "127.0.0.1" && h != "::1"
	}
	return !ip.IsLoopback()
}

func listenPortOf(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return p
}
