import test from "node:test";
import assert from "node:assert/strict";
import { formatUsage, summarizeEvents, formatComparison, mergeEvents } from "./dashboard.mjs";

test("formatUsage distinguishes missing from zero", () => {
  assert.equal(formatUsage(null, "no_usage", false).missing, true);
  assert.equal(formatUsage({ inputTokens: 0, outputTokens: 1 }, "", false).missing, false);
  assert.match(formatUsage({ inputTokens: 0, outputTokens: 1 }, "", false).text, /in 0/);
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
