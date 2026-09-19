#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

if [[ "${1:-}" == --summarize ]]; then
  dir=${2:-}
  if [[ -z "$dir" ]]; then
    echo "usage: $0 --summarize <fixture-dir>" >&2
    exit 2
  fi
  python3 "$root/scripts/summarize_x_cell.py" "$dir"
  exit 0
fi

hosts=("$@")
if ((${#hosts[@]} == 0)); then
  hosts=(claude codex grok cursor devin)
fi

if ! command -v jq >/dev/null; then
  echo "jq が必要です" >&2
  exit 2
fi

run_id=$(date +%Y%m%dT%H%M%S)-$$
out_dir="$root/artifacts/x-cell/$run_id"
mkdir -p "$out_dir"
binary="$root/bin/jev-routing"
go build -o "$binary" ./cmd/jev-routing
commit=$(git rev-parse HEAD)
prompt='internal/proxy/rewrite.go に定義されている関数（func で始まる各定義）それぞれについて、リポジトリ全体から呼び出し箇所を検索し、関数名ごとにファイル:行番号の一覧を作成してください。ファイルは変更せず、最後に `CHECK: PASS` と一行だけ出力してください。'

cleanup() {
  local dir
  for dir in "$out_dir"/*/*/worktree; do
    [[ -d "$dir" ]] && git worktree remove --force "$dir" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

# ChatGPT-login Codex rejects the short model id `terra` (HTTP 400).
# Use the config.toml id. `CODEX_MODEL` overrides. Closed stdin
# (`Reading additional input from stdin...`) is OK once the id is correct.
CODEX_MODEL=${CODEX_MODEL:-gpt-5.6-terra}

run_one() {
  local host=$1 mode=$2 case_dir worktree raw result proxy_stats started ended exit_code proxy_requests=0 proxy_chars_before=0 proxy_chars_after=0 proxy_rewritten=0
  case_dir="$out_dir/$host/$mode"
  worktree="$case_dir/worktree"
  mkdir -p "$case_dir"
  git worktree add --detach "$worktree" "$commit" >/dev/null
  raw="$case_dir/raw.json"
  result="$case_dir/result.json"
  proxy_stats="$case_dir/proxy.json"
  started=$(python3 -c 'import time; print(time.monotonic_ns())')
  set +e
  case "$host:$mode" in
    claude:baseline)
      (cd "$worktree" && claude -p --output-format json --no-session-persistence --permission-mode bypassPermissions --disallowed-tools Agent -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
    claude:jev)
      (cd "$worktree" && JEV_RUN_STATS="$proxy_stats" "$binary" run claude -- -p --output-format json --no-session-persistence --permission-mode bypassPermissions --disallowed-tools Agent -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
    codex:baseline)
      (cd "$worktree" && codex exec --json --ephemeral -s workspace-write -m "$CODEX_MODEL" "$prompt" </dev/null) >"$raw" 2>"$case_dir/stderr.log" ;;
    codex:jev)
      (cd "$worktree" && JEV_RUN_STATS="$proxy_stats" "$binary" run codex -- exec --json --ephemeral -s workspace-write -m "$CODEX_MODEL" "$prompt" </dev/null) >"$raw" 2>"$case_dir/stderr.log" ;;
    grok:baseline)
      (cd "$worktree" && grok --single "$prompt" --output-format json --no-plan --no-subagents --permission-mode bypassPermissions) >"$raw" 2>"$case_dir/stderr.log" ;;
    grok:jev)
      (cd "$worktree" && JEV_RUN_STATS="$proxy_stats" "$binary" run grok -- --single "$prompt" --output-format json --no-plan --no-subagents --permission-mode bypassPermissions) >"$raw" 2>"$case_dir/stderr.log" ;;
    cursor:baseline)
      (cd "$worktree" && cursor-agent -p --output-format json --trust --force --sandbox disabled -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
    cursor:jev)
      (cd "$worktree" && JEV_RUN_STATS="$proxy_stats" "$binary" run cursor -- -p --output-format json --trust --force --sandbox disabled -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
    devin:baseline)
      (cd "$worktree" && devin --permission-mode dangerous --respect-workspace-trust false -p -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
    devin:jev)
      (cd "$worktree" && JEV_RUN_STATS="$proxy_stats" "$binary" run devin -- --permission-mode dangerous --respect-workspace-trust false -p -- "$prompt") >"$raw" 2>"$case_dir/stderr.log" ;;
  esac
  exit_code=$?
  set -e
  ended=$(python3 -c 'import time; print(time.monotonic_ns())')
  if [[ "$mode" == jev ]]; then
    # `run` writes a final machine-readable aggregate after its child exits.
    # Do not infer proxy use from a shared cache log.
    [[ -f "$proxy_stats" ]] && proxy_requests=$(jq -r '.requests // 0' "$proxy_stats")
    [[ -f "$proxy_stats" ]] && proxy_chars_before=$(jq -r '.charsBefore // 0' "$proxy_stats")
    [[ -f "$proxy_stats" ]] && proxy_chars_after=$(jq -r '.charsAfter // 0' "$proxy_stats")
    [[ -f "$proxy_stats" ]] && proxy_rewritten=$(jq -r '.rewritten // 0' "$proxy_stats")
  fi
  python3 - "$host" "$mode" "$raw" "$result" "$started" "$ended" "$exit_code" "$proxy_requests" "$proxy_chars_before" "$proxy_chars_after" "$proxy_rewritten" "$commit" <<'PY'
import json, pathlib, sys
host, mode, raw_path, result_path, started, ended, exit_code, valid, chars_before, chars_after, rewritten, commit = sys.argv[1:]
raw = pathlib.Path(raw_path).read_text(errors="replace")
data = {}
try:
    if host == "codex":
        for line in raw.splitlines():
            event = json.loads(line)
            if event.get("type") == "turn.completed": data = event
        usage = data.get("usage", {})
        result = "".join(x.get("item", {}).get("text", "") for x in map(json.loads, raw.splitlines()) if x.get("type") == "item.completed")
    else:
        data = json.loads(raw)
        usage = data.get("usage", {})
        result = data.get("result", data.get("text", ""))
        if host == "devin" and not result:
            result = raw
except (json.JSONDecodeError, ValueError):
    usage, result = {}, raw if host == "devin" else ""
def n(*keys):
    for key in keys:
        if key in usage: return usage[key]
    return None
out = {
    "host": host, "mode": mode, "commit": commit, "exit_code": int(exit_code),
    "wall_ms": round((int(ended)-int(started))/1_000_000, 3),
    "proxy_requests": int(valid) if mode == "jev" else None,
    "proxy_observed": bool(int(valid)) if mode == "jev" else None,
    "rewritten": int(rewritten) if mode == "jev" else None,
    "routing_chars_before": int(chars_before) if mode == "jev" else None,
    "routing_chars_after": int(chars_after) if mode == "jev" else None,
    "success_marker": "CHECK: PASS" in result and not data.get("is_error", False),
    "usage": {"input_tokens": n("input_tokens", "inputTokens"), "cached_input_tokens": n("cached_input_tokens", "cache_read_input_tokens", "cacheReadTokens"), "cache_creation_input_tokens": n("cache_creation_input_tokens", "cache_write_input_tokens", "cacheWriteTokens"), "output_tokens": n("output_tokens", "outputTokens"), "reasoning_tokens": n("reasoning_tokens", "reasoningTokens")},
    "total_cost_usd": data.get("total_cost_usd", data.get("totalCostUsd")),
    "duration_api_ms": data.get("duration_api_ms", data.get("durationApiMs")),
    "num_turns": data.get("num_turns", data.get("numTurns")),
}
pathlib.Path(result_path).write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
PY
  git worktree remove --force "$worktree" >/dev/null
}

summarize() {
  local host=$1 baseline="$out_dir/$host/baseline/result.json" jev="$out_dir/$host/jev/result.json"
  jq -n --slurpfile baseline "$baseline" --slurpfile jev "$jev" '
    def delta($field): ($baseline[0][$field] - $jev[0][$field]);
    def pct($field): if $baseline[0][$field] == 0 then null else (delta($field) / $baseline[0][$field] * 100) end;
    def safe_delta($field): if ($baseline[0][$field] == null or $jev[0][$field] == null) then null else delta($field) end;
    ($jev[0].proxy_observed and (($jev[0].rewritten // 0) > 0) and $baseline[0].success_marker and $jev[0].success_marker) as $comparable |
    {host: $baseline[0].host, baseline: $baseline[0], jev: $jev[0], valid: $comparable, comparable: $comparable,
     reduction: (if $comparable then {wall_ms: delta("wall_ms"), wall_percent: pct("wall_ms"),
       output_tokens: (($baseline[0].usage.output_tokens // 0) - ($jev[0].usage.output_tokens // 0)),
       routing_request_chars: (($jev[0].routing_chars_before // 0) - ($jev[0].routing_chars_after // 0)),
       cost_usd: safe_delta("total_cost_usd"),
       duration_api_ms: safe_delta("duration_api_ms")}
       else null end),
     invalid_reason: (if $comparable then null
       elif ($jev[0].proxy_requests // 0) == 0 then "jev-routing を経由したリクエストが観測されませんでした"
       elif (($jev[0].rewritten // 0) == 0) then "書換えが 0 件のため削減は比較不能（passthrough のみ）"
       else "両条件で同じ完了条件を満たしていません" end)}
  ' >"$out_dir/$host/comparison.json"
  jq -r 'if .comparable then "\(.host): コスト差分=\(.reduction.cost_usd // "N/A")USD API時間差分=\(.reduction.duration_api_ms // "N/A")ms 書換えリクエスト削減=\(.reduction.routing_request_chars)文字 出力トークン差分=\(.reduction.output_tokens) 実行時間差分=\(.reduction.wall_ms)ms (num_turns: baseline=\(.baseline.num_turns // "N/A") jev=\(.jev.num_turns // "N/A"))" else "\(.host): 比較不能 — \(.invalid_reason)" end' "$out_dir/$host/comparison.json"
}

for host in "${hosts[@]}"; do
  case "$host" in claude|codex|grok|cursor|devin) ;; *) echo "対象は claude, codex, grok, cursor, devin です: $host" >&2; exit 2;; esac
  bin=$host; [[ $host == cursor ]] && bin=cursor-agent
  command -v "$bin" >/dev/null || { echo "$bin が PATH にありません" >&2; exit 2; }
  run_one "$host" baseline
  run_one "$host" jev
  summarize "$host"
done
echo "結果: $out_dir"
