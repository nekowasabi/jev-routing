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
prompt='ファイルを変更せず、RewriteWith、extractTools、applyCompactToMessages、DefaultOptions、DefaultUpstream の定義を調べてください。各関数について個別のツール呼び出しで定義を検索し、別のツール呼び出しで本文を読んで確認してください（合計10回以上、並列化せず順に実行）。最終回答は関数名をキー、リポジトリ相対パス:定義行番号を値にしたJSONオブジェクトだけにしてください。説明文や完了マーカーは不要です。'

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
  PYTHONPATH="$root/scripts" python3 - "$worktree" "$case_dir/expected.json" <<'PY'
import json, pathlib, sys
from summarize_x_cell import expected_live_answers
pathlib.Path(sys.argv[2]).write_text(json.dumps(expected_live_answers(pathlib.Path(sys.argv[1]))))
PY
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
      (cd "$worktree" && codex exec --json --ephemeral -s workspace-write --model "$CODEX_MODEL" -c 'model_reasoning_effort="low"' "$prompt" </dev/null) >"$raw" 2>"$case_dir/stderr.log" ;;
    codex:jev)
      (cd "$worktree" && JEV_REASONING=preserve JEV_RUN_STATS="$proxy_stats" "$binary" run codex -- exec --json --ephemeral -s workspace-write --model "$CODEX_MODEL" -c 'model_reasoning_effort="low"' "$prompt" </dev/null) >"$raw" 2>"$case_dir/stderr.log" ;;
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
  PYTHONPATH="$root/scripts" python3 - "$host" "$mode" "$raw" "$result" "$started" "$ended" "$exit_code" "$proxy_requests" "$proxy_chars_before" "$proxy_chars_after" "$proxy_rewritten" "$commit" "$worktree" "$proxy_stats" "$case_dir/expected.json" "$CODEX_MODEL" <<'PY'
import json, pathlib, sys
from summarize_x_cell import extract_cli_payload, live_quality
host, mode, raw_path, result_path, started, ended, exit_code, valid, chars_before, chars_after, rewritten, commit, worktree, proxy_path, expected_path, codex_model = sys.argv[1:]
raw = pathlib.Path(raw_path).read_text(errors="replace")
data, result = extract_cli_payload(host, raw)
usage = data.get("usage", {})
def n(*keys):
    for key in keys:
        if key in usage: return usage[key]
    return None
out = {
    "host": host, "mode": mode, "commit": commit, "exit_code": int(exit_code),
    "model": codex_model if host == "codex" else data.get("model"),
    "effort": "low" if host == "codex" else None,
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
out["quality"] = live_quality(out, result, pathlib.Path(worktree), json.loads(pathlib.Path(expected_path).read_text()))
out["proxy_stats"] = json.loads(pathlib.Path(proxy_path).read_text()) if pathlib.Path(proxy_path).exists() else {}
out["routing_reasoning"] = out["proxy_stats"].get("reasoning") if mode == "jev" else None
pathlib.Path(result_path).write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
PY
  git worktree remove --force "$worktree" >/dev/null
}

summarize() {
  local host=$1 baseline="$out_dir/$host/baseline/result.json" jev="$out_dir/$host/jev/result.json"
  local acceptance
  acceptance=$(PYTHONPATH="$root/scripts" python3 - "$baseline" "$jev" <<'PY'
import json, pathlib, sys
from summarize_x_cell import live_acceptance
print(json.dumps(live_acceptance(*(json.loads(pathlib.Path(p).read_text()) for p in sys.argv[1:]))))
PY
)
  jq -n --slurpfile baseline "$baseline" --slurpfile jev "$jev" --argjson acceptance "$acceptance" '
    def delta($field): ($baseline[0][$field] - $jev[0][$field]);
    def pct($field): if $baseline[0][$field] == 0 then null else (delta($field) / $baseline[0][$field] * 100) end;
    def safe_delta($field): if ($baseline[0][$field] == null or $jev[0][$field] == null) then null else delta($field) end;
    $acceptance.valid as $comparable |
    {host: $baseline[0].host, baseline: $baseline[0], jev: $jev[0], valid: $comparable, comparable: $comparable,
     reduction: (if $comparable then {wall_ms: delta("wall_ms"), wall_percent: pct("wall_ms"),
       output_tokens: (($baseline[0].usage.output_tokens // 0) - ($jev[0].usage.output_tokens // 0)),
       routing_request_chars: (($jev[0].routing_chars_before // 0) - ($jev[0].routing_chars_after // 0)),
       cost_usd: safe_delta("total_cost_usd"),
       duration_api_ms: safe_delta("duration_api_ms")}
       else null end),
     invalid_reason: (if $comparable then null else ($acceptance.failures | join(", ")) end)}
  ' >"$out_dir/$host/comparison.json"
  jq -r 'if .comparable then "\(.host): コスト差分=\(.reduction.cost_usd // "N/A")USD API時間差分=\(.reduction.duration_api_ms // "N/A")ms 書換えリクエスト削減=\(.reduction.routing_request_chars)文字 出力トークン差分=\(.reduction.output_tokens) 実行時間差分=\(.reduction.wall_ms)ms (num_turns: baseline=\(.baseline.num_turns // "N/A") jev=\(.jev.num_turns // "N/A"))" else "\(.host): 比較不能 — \(.invalid_reason)" end' "$out_dir/$host/comparison.json"
  jq -e '.valid == true' "$out_dir/$host/comparison.json" >/dev/null
}

failed=0
for host in "${hosts[@]}"; do
  case "$host" in claude|codex|grok|cursor|devin) ;; *) echo "対象は claude, codex, grok, cursor, devin です: $host" >&2; exit 2;; esac
  bin=$host; [[ $host == cursor ]] && bin=cursor-agent
  command -v "$bin" >/dev/null || { echo "$bin が PATH にありません" >&2; exit 2; }
  run_one "$host" baseline
  run_one "$host" jev
  summarize "$host" || failed=1
done
echo "結果: $out_dir"
exit "$failed"
