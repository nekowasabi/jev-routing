/* jev-routing read-only dashboard. No secrets, no write APIs. */
(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory();
  } else {
    root.JevDashboard = factory();
    root.JevDashboard.start();
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  const MAX_STORE = 1000;
  const TABLE_ROWS = 200;

  function clip(s, n) {
    s = String(s == null ? "" : s);
    return s.length <= n ? s : s.slice(0, n);
  }

  function formatUsage(u, missing, partial) {
    if (!u) {
      return { text: missing ? "missing: " + missing : "missing", missing: true };
    }
    const parts = [];
    if (u.inputTokens != null) parts.push("in " + u.inputTokens);
    if (u.outputTokens != null) parts.push("out " + u.outputTokens);
    if (u.cachedTokens != null) parts.push("cache " + u.cachedTokens);
    if (u.reasoningTokens != null) parts.push("reason " + u.reasoningTokens);
    if (partial) parts.push("partial");
    if (!parts.length) return { text: "missing", missing: true };
    return { text: parts.join(" · "), missing: false };
  }

  function usageTotals(events) {
    const totals = { input: 0, output: 0, cached: 0, cacheWrite: 0, reasoning: 0, known: 0 };
    for (const e of events || []) {
      const u = e.usage;
      if (!u) continue;
      totals.known++;
      totals.input += Number(u.inputTokens) || 0;
      totals.output += Number(u.outputTokens) || 0;
      totals.cached += Number(u.cachedTokens) || 0;
      totals.cacheWrite += Number(u.cacheWriteTokens) || 0;
      totals.reasoning += Number(u.reasoningTokens) || 0;
    }
    return totals;
  }

  function toolReplacement(e) {
    if (!e.changed || !Array.isArray(e.toolsBefore) || !Array.isArray(e.toolsAfter)) return "—";
    const removed = e.toolsBefore.filter((name) => !e.toolsAfter.includes(name));
    const kept = e.toolsAfter.join(", ");
    return (removed.length ? "除外: " + removed.join(", ") : "変更") + (kept ? " → 採用: " + kept : "");
  }

  function summarizeUnsupportedHistory(events) {
    const out = {};
    for (const e of events || []) {
      for (const shape of e.historyIssues || e.unsupportedHistory || []) out[shape] = (out[shape] || 0) + 1;
    }
    return out;
  }

  function formatConfidence(value) {
    return value == null ? "—" : (Number(value) * 100).toFixed(0) + "%";
  }

  function jevSkipReason(e) {
    if ((e.selectionJevCalls || 0) > 0) return "ツール選定でJev実行";
    if (e.source === "local") return "ローカル分類で採用";
    if ((e.otherJevCalls || 0) > 0) return "選定外でJev実行";
    return e.reason ? "安全側: " + e.reason : "未記録";
  }

  function routeOutcome(e) {
    if (e.reason === "no_tool_needed") return "Jev がツール不要と判断";
    if (e.source === "jev" && e.changed) return "Jev 分類で採用";
    if (e.source === "local" && e.changed) return "ローカル分類で採用";
    if (e.reason === "unrecognized_format") return "履歴形式が未対応のため通過";
    if (e.reason === "uncertain_jev") return "Jev 判定が不確実のため通過";
    if (e.reason) return "安全側通過: " + e.reason;
    return "未記録";
  }

  function skippedTools(events) {
    const out = {};
    for (const e of events || []) {
      if (!e.changed || !Array.isArray(e.toolsBefore) || !Array.isArray(e.toolsAfter)) continue;
      for (const name of e.toolsBefore) if (!e.toolsAfter.includes(name)) out[name] = (out[name] || 0) + 1;
    }
    return out;
  }

  function summarizeEvents(events, counts) {
    const out = Object.assign(
      { local: 0, jev: 0, passthrough: 0, filter: 0, forced: 0, direct: 0, ineligible: 0 },
      counts || {}
    );
    for (const e of events || []) {
      if (e.source === "local") out.local++;
      else if (e.source === "jev") out.jev++;
      else if (e.source === "passthrough") out.passthrough++;
      if (e.apply === "filter") out.filter++;
      else if (e.apply === "forced") out.forced++;
      else if (e.apply === "direct") out.direct++;
      if (e.reason && String(e.reason).startsWith("passthrough") === false && e.apply === "none") {
        out.ineligible++;
      }
    }
    return out;
  }

  function formatComparison(jsonText) {
    let data;
    try {
      data = JSON.parse(jsonText);
    } catch (err) {
      return { ok: false, error: "invalid json" };
    }
    if (data == null || typeof data !== "object" || Array.isArray(data)) {
      return { ok: false, error: "unexpected structure" };
    }
    return { ok: true, data };
  }

  function mergeEvents(store, incoming, oldestSeq, truncated) {
    const next = store.slice();
    for (const e of incoming || []) {
      if (!next.some((x) => x.seq === e.seq)) next.push(e);
    }
    next.sort((a, b) => a.seq - b.seq);
    while (next.length > MAX_STORE) next.shift();
    return { events: next, truncated: !!truncated, oldestSeq: oldestSeq || (next[0] && next[0].seq) || 0 };
  }

  function start() {
    const status = document.getElementById("status");
    const rows = document.getElementById("rows");
    const countsEl = document.getElementById("counts");
    const usageEl = document.getElementById("usage-summary");
    const historyEl = document.getElementById("history-summary");
    const toolChart = document.getElementById("tool-chart");
    const toolLegend = document.getElementById("tool-legend");
    const cmp = document.getElementById("cmp");
    const cmpOut = document.getElementById("cmp-out");
    if (!status || !rows) return;
    let store = [];
    let since = 0;

    function text(el, value) {
      el.textContent = value == null ? "" : String(value);
    }

    function render(payload) {
      if (!payload) {
      text(status, "履歴を取得できません");
        return;
      }
      const r = payload.router || {};
      const merged = mergeEvents(store, payload.events, r.oldestSeq, payload.historyTruncated);
      store = merged.events;
      if (store.length) since = store[store.length - 1].seq;
      const summary = summarizeEvents(store, r.counts);
      const totals = usageTotals(store);
      const unsupported = summarizeUnsupportedHistory(store);
      const skipped = skippedTools(store);
      text(
        status,
        "インスタンス " +
          (r.instanceId || "?") +
          " · 記録 " +
          (r.recorded != null ? r.recorded : store.length) +
          (payload.historyTruncated ? " · 履歴を省略" : "") +
          (r.mode ? " · モード " + r.mode : "")
      );
      countsEl.replaceChildren();
      for (const [k, v] of Object.entries(summary)) {
        const dt = document.createElement("dt");
        text(dt, k);
        const dd = document.createElement("dd");
        text(dd, v);
        countsEl.append(dt, dd);
      }
      if (usageEl) {
        const metrics = [["入力", totals.input, "input"], ["出力", totals.output, "output"], ["キャッシュ読取", totals.cached, "cached"], ["キャッシュ書込", totals.cacheWrite, "cached"], ["推論", totals.reasoning, "reasoning"]];
        const max = Math.max(...metrics.map(([, value]) => value), 1);
        usageEl.replaceChildren();
        for (const [label, value, kind] of metrics) {
          const item = document.createElement("div");
          item.className = "usage-bar " + kind;
          const name = document.createElement("span");
          const meter = document.createElement("i");
          const amount = document.createElement("b");
          text(name, label);
          meter.style.width = (value / max) * 100 + "%";
          text(amount, value.toLocaleString() + " トークン");
          item.append(name, meter, amount);
          usageEl.appendChild(item);
        }
        const note = document.createElement("small");
        text(note, totals.known + " 件の上流レスポンスから集計");
        usageEl.appendChild(note);
      }
      if (historyEl) {
        historyEl.replaceChildren();
        const entries = Object.entries(unsupported).sort((a, b) => b[1] - a[1]);
        if (!entries.length) {
          text(historyEl, "未対応の履歴形式はまだ観測されていません");
        } else {
          for (const [shape, count] of entries) {
            const item = document.createElement("span");
            item.className = "history-chip";
            text(item, shape + " · " + count + " 件");
            historyEl.appendChild(item);
          }
        }
      }
      if (toolChart && toolLegend) {
        const entries = Object.entries(skipped).sort((a, b) => b[1] - a[1]);
        const palette = ["#4b6fff", "#8b5cf6", "#12b8a6", "#f59e0b", "#ef476f", "#06b6d4"];
        const total = entries.reduce((sum, [, count]) => sum + count, 0);
        toolLegend.replaceChildren();
        if (!total) {
          toolChart.style.background = "#e5e8f0";
          text(toolLegend, "除外されたツールはまだありません");
        } else {
          let at = 0;
          const slices = entries.map(([, count], i) => {
            const start = at;
            at += (count / total) * 100;
            return palette[i % palette.length] + " " + start + "% " + at + "%";
          });
          toolChart.style.background = "conic-gradient(" + slices.join(",") + ")";
          entries.forEach(([name, count], i) => {
            const item = document.createElement("span");
            item.className = "tool-legend-item";
            item.style.setProperty("--swatch", palette[i % palette.length]);
            text(item, name + " · " + count + " 件");
            toolLegend.appendChild(item);
          });
        }
      }
      rows.replaceChildren();
      const shown = store.slice(-TABLE_ROWS).reverse();
      for (const e of shown) {
        const tr = document.createElement("tr");
        const usage = formatUsage(e.usage, e.usageMissing, e.usagePartial);
        const vals = [
          e.seq,
          e.source,
          e.apply,
          routeOutcome(e),
          e.chosen,
          e.reason,
          formatConfidence(e.confidence),
          jevSkipReason(e),
          e.changed ? "yes" : "no",
          toolReplacement(e),
          e.jevCalls != null ? e.jevCalls : "",
          usage.text,
          e.headerMs != null ? e.headerMs + "ms" : e.bodyMs != null ? e.bodyMs + "ms" : "missing",
        ];
        for (const v of vals) {
          const td = document.createElement("td");
          text(td, v);
          if (usage.missing && v === usage.text) td.className = "missing";
          tr.appendChild(td);
        }
        rows.appendChild(tr);
      }
    }

    async function poll() {
      try {
        const res = await fetch("/dashboard/events?since=" + encodeURIComponent(since), {
          headers: { accept: "application/json" },
        });
        if (!res.ok) {
          text(status, "履歴を取得できません (" + res.status + ")");
          return;
        }
        render(await res.json());
      } catch (err) {
        text(status, "履歴を取得できません");
      }
    }

    if (cmp && cmpOut) {
      cmp.addEventListener("input", function () {
        const got = formatComparison(cmp.value);
        if (!got.ok) {
          cmpOut.className = "err";
          text(cmpOut, got.error);
          return;
        }
        cmpOut.className = "";
        text(cmpOut, JSON.stringify(got.data, null, 2));
      });
    }
    poll();
    setInterval(poll, 2000);
  }

  return { clip, formatUsage, usageTotals, toolReplacement, summarizeUnsupportedHistory, formatConfidence, jevSkipReason, routeOutcome, skippedTools, summarizeEvents, formatComparison, mergeEvents, start };
});
