// jev-routing is a request-rewriting proxy for Claude Code, Codex, and Grok Build.
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
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
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
  jev-routing run claude|codex|grok [-- host-args...]
  jev-routing serve --host claude|codex|grok [--listen 127.0.0.1:8787]
  jev-routing compact < transcript.json
  jev-routing bench --host grok

Environment:
  TYPESAFE_API_KEY / JEV_API_KEY   Jev key (optional; local classifier otherwise)
  JEV_LISTEN                       default 127.0.0.1:8787
`)
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	hostName := fs.String("host", "claude", "claude | codex | grok")
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
	client := jev.FromEnv()
	srv, err := proxy.New(listen, h, client)
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
		fmt.Fprintln(os.Stderr, "usage: jev-routing run claude|codex|grok")
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
	listen := envOr("JEV_LISTEN", "127.0.0.1:8787")
	client := jev.FromEnv()
	srv, err := proxy.New(listen, h, client)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	defer httpSrv.Close()
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
	cmd := exec.Command(path, rest...)
	cmd.Env = host.ChildEnv(h, listen)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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
	hostName := fs.String("host", "claude", "claude | codex | grok")
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
		fmt.Fprintf(os.Stderr, "  # ~/.codex/config.toml\n  model_provider = \"jev\"\n  [model_providers.jev]\n  base_url = \"http://%s/v1\"\n  wire_api = \"responses\"\n", listen)
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
