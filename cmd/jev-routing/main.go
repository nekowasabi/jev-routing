// jev-routing is a request-rewriting proxy for Claude Code, Codex, Grok Build, Cursor, and Devin.
//
// It is not an MCP server. Do not `claude mcp add` / `codex mcp add` / `grok mcp add`.
// Install the binary and wrap the host:
//
//	go install github.com/nekowasabi/jev-routing/cmd/jev-routing@latest
//	export TYPESAFE_API_KEY=ts_...
//	jev-routing run grok
//	jev-routing run claude
//	jev-routing run codex
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
	"github.com/nekowasabi/jev-routing/internal/plan"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "serve":
		os.Exit(cmdServe(os.Args[2:]))
	case "compact":
		os.Exit(cmdCompact(os.Args[2:]))
	case "bench":
		os.Exit(cmdBench(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `jev-routing — Jev harness (Go). No npx. No MCP.

Commands:
  jev-routing run claude|codex|grok|cursor|devin [-- host-args...]
  jev-routing serve --host claude|codex|grok|cursor|devin [--listen 127.0.0.1:8787]
  jev-routing compact < transcript.json
  jev-routing bench --host grok

Environment:
  TYPESAFE_API_KEY / JEV_API_KEY   Jev key (optional; local classifier otherwise)
  JEV_LISTEN                       preferred bind address (run falls back to an available port)
  JEV_ROUTING_MODE                 baseline | filter | forced (default filter)
  JEV_COMPACTION                   off | on (default on)
  JEV_REASONING                    preserve | legacy (default legacy)
  JEV_ARGS_MODEL / JEV_ARGS_TOOLS  optional forced-only arg model split
  JEV_DIRECT_TOOLS                 optional forced-only constant-arg Chat tools
  JEV_RUN_ID                       optional comparison id
  JEV_RUN_STATS                    path for process-end JSON stats
`)
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	hostName := fs.String("host", "claude", "claude | codex | grok | cursor | devin")
	listen := fs.String("listen", envOr("JEV_LISTEN", "127.0.0.1:8787"), "bind address")
	_ = fs.Parse(args)
	h, err := host.Parse(*hostName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return serve(h, *listen)
}

func serve(h host.ID, listen string) int {
	opt, err := proxy.OptionsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	client := jev.FromEnv()
	srv, err := proxy.NewWithOptions(listen, h, client, nil, opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "jev-routing proxy for %s on http://%s (upstream %s, engine %s)\n",
		h.Label(), listen, proxy.DefaultUpstream(h), engineName(client))
	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		_ = httpSrv.Close()
	}()
	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdRun(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-routing run claude|codex|grok|cursor|devin")
		return 2
	}
	h, err := host.Parse(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	opt, err := proxy.OptionsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	client := jev.FromEnv()
	// Why: the child owns the terminal in raw mode; async proxy logs on the
	// shared stderr fd would corrupt its TUI. Best effort — never fail the run.
	logFile, logPath := openRunLog()
	var logW io.Writer // nil interface, not a typed-nil *os.File
	if logFile != nil {
		defer logFile.Close()
		logW = logFile
	}
	ln, err := listenForRun()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	listen := ln.Addr().String()
	srv, err := proxy.NewWithOptions(listen, h, client, logW, opt)
	if err != nil {
		_ = ln.Close()
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	defer httpSrv.Close()
	if logPath != "" {
		fmt.Fprintf(os.Stderr, "jev-routing: logging to %s\n", logPath)
	}
	if err := waitHealthy("http://" + listen + "/healthz"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	bin := h.Binary()
	path, err := exec.LookPath(bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy is up at http://%s but %s is not on PATH: %v\n", listen, bin, err)
		fmt.Fprintf(os.Stderr, "leave this running, or install %s, then set the host env:\n", bin)
		printEnvHint(h, listen)
		<-make(chan struct{})
	}
	cmd := exec.Command(path, append(host.ChildArgs(h, listen), rest...)...)
	cmd.Env = host.ChildEnv(h, listen)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		writeRunStats(srv)
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	writeRunStats(srv)
	return 0
}

func writeRunStats(srv *proxy.Server) {
	path := os.Getenv("JEV_RUN_STATS")
	if path == "" {
		return
	}
	raw, err := json.Marshal(srv.RunStats())
	if err != nil {
		fmt.Fprintf(os.Stderr, "jev-routing: failed to encode run stats: %v\n", err)
		return
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "jev-routing: failed to write run stats: %v\n", err)
	}
}

func listenForRun() (net.Listener, error) {
	if listen := os.Getenv("JEV_LISTEN"); listen != "" {
		return net.Listen("tcp", listen)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:8787")
	if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		return ln, err
	}
	// Why: Retain 8787 for existing manual configurations; only concurrent runs need a private port.
	return net.Listen("tcp", "127.0.0.1:0")
}

// openRunLog opens the append-mode log for `run`. Returns (nil, "") on failure,
// which makes the caller fall back to stderr.
func openRunLog() (*os.File, string) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "jev-routing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, ""
	}
	path := filepath.Join(dir, "run.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, ""
	}
	return f, path
}

func cmdCompact(args []string) int {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var items []compact.Item
	if err := json.Unmarshal(raw, &items); err != nil {
		fmt.Fprintln(os.Stderr, "expected JSON array of compact items:", err)
		return 2
	}
	res := compact.CompactLocal(items, compact.Options{})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	fmt.Fprintf(os.Stderr, "dropped %d truncated %d saved %d chars (ratio %.2f)\n",
		res.Stats.Dropped, res.Stats.Truncated, res.Stats.CharsDropped, compact.ReductionRatio(res))
	return 0
}

func cmdBench(args []string) int {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	hostName := fs.String("host", "claude", "claude | codex | grok | cursor | devin")
	_ = fs.Parse(args)
	h, err := host.Parse(*hostName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	prompts := []string{
		"The auth middleware test is failing. Find the test, read it, fix the assertion in place, and re-run the tests.",
		"There is a typo 'recieve' somewhere in the repo. Grep for it and fix it with an in-place edit.",
		"Review GitHub PR 842. Fetch the PR, read the changed local files, and leave a review comment. Do not open a new pull request.",
	}
	for _, p := range prompts {
		d := plan.Decide(p, nil, nil, h)
		fmt.Printf("%s\t%s\tconf=%.2f done=%.2f\n", h, d.Tool, d.Confidence, d.Done)
	}
	return 0
}

func waitHealthy(url string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not become healthy at %s", url)
}

func printEnvHint(h host.ID, listen string) {
	switch h {
	case host.Claude:
		fmt.Fprintf(os.Stderr, "  unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN\n  export ANTHROPIC_BASE_URL=http://%s\n  claude\n", listen)
	case host.Grok:
		fmt.Fprintf(os.Stderr, "  unset XAI_API_KEY GROK_MODELS_BASE_URL\n  export GROK_CLI_CHAT_PROXY_BASE_URL=http://%s/v1\n  grok\n", listen)
	case host.Codex:
		fmt.Fprintf(os.Stderr, "  # ~/.codex/config.toml\n  openai_base_url = \"http://%s/v1\"\n", listen)
	case host.Cursor:
		fmt.Fprintf(os.Stderr, "  export CURSOR_API_ENDPOINT=http://%s\n  cursor-agent --endpoint http://%s\n", listen, listen)
	case host.Devin:
		fmt.Fprintf(os.Stderr, "  export DEVIN_API_URL=http://%s\n  devin\n", listen)
	}
}

func engineName(c *jev.Client) string {
	if c != nil && c.Live() {
		return "live-jev"
	}
	return "local"
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
