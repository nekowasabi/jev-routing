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
        text(status, "Failed to load events");
        return;
      }
      const r = payload.router || {};
      const merged = mergeEvents(store, payload.events, r.oldestSeq, payload.historyTruncated);
      store = merged.events;
      if (store.length) since = store[store.length - 1].seq;
      const summary = summarizeEvents(store, r.counts);
      text(
        status,
        "instance " +
          (r.instanceId || "?") +
          " · recorded " +
          (r.recorded != null ? r.recorded : store.length) +
          (payload.historyTruncated ? " · history truncated" : "") +
          (r.mode ? " · mode " + r.mode : "")
      );
      countsEl.replaceChildren();
      for (const [k, v] of Object.entries(summary)) {
        const dt = document.createElement("dt");
        text(dt, k);
        const dd = document.createElement("dd");
        text(dd, v);
        countsEl.append(dt, dd);
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
          e.chosen,
          e.reason,
          e.changed ? "yes" : "no",
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
          text(status, "Failed to load events (" + res.status + ")");
          return;
        }
        render(await res.json());
      } catch (err) {
        text(status, "Failed to load events");
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

  return { clip, formatUsage, summarizeEvents, formatComparison, mergeEvents, start };
});
