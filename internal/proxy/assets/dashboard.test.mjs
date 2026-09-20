import test from "node:test";
import assert from "node:assert/strict";
import { formatUsage, usageTotals, toolReplacement, summarizeUnsupportedHistory, unknownHistoryDetails, formatConfidence, routeOutcome, skippedTools, summarizeEvents, formatComparison, mergeEvents } from "./dashboard.mjs";

test("formatUsage distinguishes missing from zero", () => {
  assert.equal(formatUsage(null, "no_usage", false).missing, true);
  assert.equal(formatUsage({ inputTokens: 0, outputTokens: 1 }, "", false).missing, false);
  assert.match(formatUsage({ inputTokens: 0, outputTokens: 1 }, "", false).text, /in 0/);
});

test("usageTotals adds reported and saved token categories", () => {
  assert.deepEqual(usageTotals([{ usage: { inputTokens: 3, outputTokens: 2, cachedTokens: 1, reasoningTokens: 4 }, savedTokens: { directInput: 6 } }, { usage: { inputTokens: 5 }, savedTokens: { compactionInput: 7 } }]), { input: 8, output: 2, cached: 1, cacheWrite: 0, reasoning: 4, known: 2, directSaved: 6, compactionSaved: 7 });
});

test("unknownHistoryDetails keeps the issue and affected request numbers", () => {
  assert.deepEqual(unknownHistoryDetails([{ seq: 3, historyIssues: ["item:tool_addition"] }, { seq: 7, historyIssues: ["item:tool_addition", "message:<missing>"] }]), {
    "item:tool_addition": { count: 2, requests: [3, 7] },
    "message:<missing>": { count: 1, requests: [7] }
  });
});

test("toolReplacement identifies removed and selected tools", () => {
  assert.equal(toolReplacement({ changed: true, toolsBefore: ["Read", "Grep"], toolsAfter: ["Grep"] }), "除外: Read → 採用: Grep");
  assert.equal(toolReplacement({ changed: false, toolsBefore: ["Read"], toolsAfter: ["Read"] }), "—");
});

test("summarizeUnsupportedHistory counts only recorded shapes", () => {
  assert.deepEqual(summarizeUnsupportedHistory([{ unsupportedHistory: ["item:local_shell_call"] }, { unsupportedHistory: ["item:local_shell_call", "content:refusal"] }, {}]), { "item:local_shell_call": 2, "content:refusal": 1 });
});

test("routeOutcome distinguishes routing outcomes", () => {
  assert.equal(formatConfidence(0.82), "82%");
  assert.equal(routeOutcome({ source: "local", changed: true, selectionJevCalls: 1 }), "Jev を使ったがローカル分類を採用");
  assert.equal(routeOutcome({ source: "passthrough", reason: "unrecognized_format" }), "履歴形式が未対応のため通過");
  assert.deepEqual(skippedTools([{ changed: true, toolsBefore: ["Read", "Grep"], toolsAfter: ["Grep"] }]), { Read: 1 });
});

test("summarizeEvents counts apply modes", () => {
  const s = summarizeEvents([
    { source: "local", apply: "filter" },
    { source: "jev", apply: "forced" },
    { source: "passthrough", apply: "none" },
    { source: "jev", apply: "direct" },
  ]);
  assert.equal(s.local, 1);
  assert.equal(s.jev, 2);
  assert.equal(s.filter, 1);
  assert.equal(s.forced, 1);
  assert.equal(s.direct, 1);
});

test("formatComparison rejects html-like junk", () => {
  const bad = formatComparison("<script>alert(1)</script>");
  assert.equal(bad.ok, false);
  const good = formatComparison('{"groups":[{"id":"a"}]}');
  assert.equal(good.ok, true);
  assert.equal(good.data.groups[0].id, "a");
});

test("mergeEvents caps store and flags truncation", () => {
  const incoming = [];
  for (let i = 1; i <= 3; i++) incoming.push({ seq: i });
  const got = mergeEvents([], incoming, 1, true);
  assert.equal(got.truncated, true);
  assert.equal(got.events.length, 3);
});
