#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

if [[ "${1:-}" == --summarize ]]; then
  [[ -n "${2:-}" ]] || { echo "usage: $0 --summarize <legacy-fixture-dir>" >&2; exit 2; }
  exec python3 "$root/scripts/summarize_x_cell.py" "$2"
fi

task=${JEV_XCELL_TASK:-locate}
case "$task" in
  locate|module) ;;
  *) echo "JEV_XCELL_TASK は locate または module です: $task" >&2; exit 2 ;;
esac
repeats=${JEV_XCELL_REPEATS:-1}
[[ "$repeats" =~ ^[1-9][0-9]*$ ]] || { echo "JEV_XCELL_REPEATS は 1 以上の整数です: $repeats" >&2; exit 2; }
if [[ "${JEV_SELECTION_BENCHMARK:-0}" == 1 ]]; then
  echo "JEV_SELECTION_BENCHMARK は廃止しました。共通 bench の --modes direct,off,on を使用してください" >&2
  exit 2
fi

hosts=("$@")
if ((${#hosts[@]} == 0)); then hosts=(claude codex grok devin); fi
suite="$root/artifacts/x-cell/$(date +%Y%m%dT%H%M%S)-$$"
mkdir -p "$suite"
binary="$suite/jev-routing"
go build -o "$binary" ./cmd/jev-routing
modes=off,on
if [[ "${JEV_XCELL_DIRECT:-0}" == 1 ]]; then modes=direct,off,on; fi
failed=0

for host in "${hosts[@]}"; do
  case "$host" in claude|codex|grok|devin) ;; *) echo "対象は claude, codex, grok, devin です: $host" >&2; exit 2 ;; esac
  command -v "$host" >/dev/null || { echo "$host が PATH にありません" >&2; exit 2; }
  args=(bench --agent "$host" --tasks "xcell-$task" --modes "$modes" --reps "$repeats" --out "$suite/$host")
  case "$host" in
    claude) args+=(--model "${CLAUDE_MODEL:-claude-sonnet-5}" --effort medium) ;;
    codex) args+=(--model "${CODEX_MODEL:-gpt-5.6-terra}" --effort medium) ;;
    devin) args+=(--model "${DEVIN_MODEL:-gpt-5-6-terra-medium}") ;;
  esac
  if ! "$binary" "${args[@]}"; then failed=1; fi
done

echo "結果: $suite"
exit "$failed"
