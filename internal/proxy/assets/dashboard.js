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
  const STRINGS = {
    ja: {
      language: "表示言語", routingOverview: "ルーティング概要", tokenUsage: "トークン消費",
      route: "判定経路", routeHelp: "どの判断元を通ったか", apply: "適用方式", applyHelp: "ツール一覧をどう扱ったか",
      local: "ローカル判定", jev: "Jev 判定", passthrough: "安全側通過", ineligible: "対象外", filter: "絞り込み適用", forced: "強制適用", direct: "そのまま送信", jev_failed: "Jev 呼び出し失敗",
      loading: "読み込み中…", unavailable: "履歴を取得できません", disconnected: "接続切れ", instance: "インスタンス", recorded: "記録", mode: "モード", updated: "最終更新", historyTruncated: "履歴を省略", sample: "サンプル",
      input: "入力", output: "出力", cacheRead: "キャッシュ読取", cacheWrite: "キャッシュ書込", reasoning: "推論", directSaved: "直通で回避した入力（推定）", compactSaved: "コンパクションで削減した入力（推定）", tokens: "トークン", usageNote: "件の上流レスポンスから実測。回避・削減は送信前の推定値。",
      history: "安全側へ通過した履歴（不明な箇所）", skippedTools: "カタログから除外したツール", application: "可動個所と適用", unapplied: "未適用の理由", effects: "比較効果", funnel: "判断が届いたか", kinds: "Jevが働いた場所", kindsHelp: "スキル・MCP・プラグイン・モデルeffortを含む六分類と履歴圧縮。件数0でも「未観測／観察のみ／無効」を出します。", apps: "適用した操作", kind: "分類", appState: "状態", capability: "能力", call: "操作", events: "直近のリクエスト", host: "ホスト", source: "判定元", all: "すべて", reconnect: "再接続", sequence: "連番", outcome: "判定結果", chosenTool: "採用ツール", reason: "理由", confidence: "確信度", replacement: "ツール置換", time: "時間",
      class_model: "モデルとeffort", class_subagent: "子エージェント", class_skill: "スキル", class_mcp_tool: "MCPツール", class_cli: "CLI", class_plugin: "プラグイン", class_compaction: "履歴圧縮",
      st_verified: "成果確認", st_delivered: "配達済み", st_started: "開始（結果待ち）", st_rewritten: "リクエストを書き換え", st_failed: "失敗", st_selected: "判断のみ", st_observe: "観察のみ（未適用）", st_off: "無効", st_unsupported: "未対応", st_unobserved: "未観測",
      noTool: "Jev がツール不要と判断", jevSelected: "Jev 分類で採用", localSelected: "ローカル分類で採用", localAfterJev: "Jev を使ったがローカル分類を採用", unsupported: "履歴形式が未対応のため通過", uncertain: "Jev 判定が不確実のため通過", safePass: "安全側通過: ", unrecorded: "未記録", missing: "欠測", changed: "変更", yes: "あり", no: "なし"
    },
    en: {
      language: "Language", routingOverview: "Routing overview", tokenUsage: "Token usage",
      route: "Decision source", routeHelp: "Where each routing decision came from", apply: "Application", applyHelp: "How the tool catalog was handled",
      local: "Local decision", jev: "Jev decision", passthrough: "Safe pass-through", ineligible: "Ineligible", filter: "Filtered", forced: "Forced", direct: "Sent unchanged", jev_failed: "Jev call failures",
      loading: "Loading…", unavailable: "Could not load history", disconnected: "Disconnected", instance: "Instance", recorded: "Recorded", mode: "Mode", updated: "Updated", historyTruncated: "History truncated", sample: "Sample",
      input: "Input", output: "Output", cacheRead: "Cache read", cacheWrite: "Cache write", reasoning: "Reasoning", directSaved: "Input avoided directly (estimated)", compactSaved: "Input saved by compaction (estimated)", tokens: "tokens", usageNote: "upstream responses measured. Avoided and saved values are pre-send estimates.",
      history: "History passed through safely (unknown parts)", skippedTools: "Tools excluded from the catalog", application: "Applications", unapplied: "Why not applied", effects: "Comparison effects", funnel: "Did the decision arrive?", kinds: "Where Jev ran", kindsHelp: "Six classes plus compaction, including skills, MCP, plugins, and model effort. Zero counts still show unobserved / observe-only / off.", apps: "Applied operations", kind: "Class", appState: "State", capability: "Capability", call: "Call", events: "Recent requests", host: "Host", source: "Decision source", all: "All", reconnect: "Reconnect", sequence: "Sequence", outcome: "Outcome", chosenTool: "Chosen tool", reason: "Reason", confidence: "Confidence", replacement: "Tool replacement", time: "Time",
      class_model: "Model and effort", class_subagent: "Subagents", class_skill: "Skills", class_mcp_tool: "MCP tools", class_cli: "CLIs", class_plugin: "Plugins", class_compaction: "History compaction",
      st_verified: "Verified", st_delivered: "Delivered", st_started: "Started", st_rewritten: "Request rewritten", st_failed: "Failed", st_selected: "Selected only", st_observe: "Observe only", st_off: "Off", st_unsupported: "Unsupported", st_unobserved: "Unobserved",
      noTool: "Jev determined no tool is needed", jevSelected: "Selected by Jev", localSelected: "Selected locally", localAfterJev: "Jev consulted; local selection used", unsupported: "Passed through: unsupported history format", uncertain: "Passed through: Jev decision uncertain", safePass: "Safe pass-through: ", unrecorded: "Not recorded", missing: "missing", changed: "changed", yes: "yes", no: "no"
    }
  };

  function t(key, lang) {
    const values = STRINGS[lang] || STRINGS.ja;
    return values[key] || STRINGS.ja[key] || key;
  }

  function overviewGroups(summary, lang) {
    // Why: Keep decision source separate from application mode so a pass-through is not mistaken for a Jev failure.
    return [
      { title: t("route", lang), help: t("routeHelp", lang), values: ["local", "jev", "passthrough", "ineligible"] },
      { title: t("apply", lang), help: t("applyHelp", lang), values: ["filter", "forced", "direct", "jev_failed"] }
    ].map(function (group) {
      return Object.assign(group, { values: group.values.map(function (key) { return { key: key, label: t(key, lang), value: Number(summary[key]) || 0 }; }) });
    });
  }

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

  function formatSavings(saved) {
    if (!saved) return "";
    const parts = [];
    if (saved.directInput) parts.push("直通回避 in " + saved.directInput + "（推定）");
    if (saved.compactionInput) parts.push("圧縮削減 in " + saved.compactionInput + "（推定）");
    return parts.join(" · ");
  }

  function usageTotals(events) {
    const totals = { input: 0, output: 0, cached: 0, cacheWrite: 0, reasoning: 0, known: 0, directSaved: 0, compactionSaved: 0 };
    for (const e of events || []) {
	  const saved = e.savedTokens || {};
	  totals.directSaved += Number(saved.directInput) || 0;
	  totals.compactionSaved += Number(saved.compactionInput) || 0;
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

  function unknownHistoryDetails(events) {
    const out = {};
    for (const e of events || []) {
      for (const issue of e.historyIssues || e.unsupportedHistory || []) {
        const detail = out[issue] || (out[issue] = { count: 0, requests: [] });
        detail.count++;
        if (e.seq != null && !detail.requests.includes(e.seq)) detail.requests.push(e.seq);
      }
    }
    return out;
  }

  function formatConfidence(value) {
    return value == null ? "—" : (Number(value) * 100).toFixed(0) + "%";
  }

  function routeOutcome(e, lang) {
    if (e.reason === "no_tool_needed") return t("noTool", lang);
    if (e.source === "jev" && e.changed) return t("jevSelected", lang);
    if (e.source === "local" && e.changed) {
      return (e.selectionJevCalls || 0) > 0 ? t("localAfterJev", lang) : t("localSelected", lang);
    }
    if (e.reason === "unrecognized_format") return t("unsupported", lang);
    if (e.reason === "uncertain_jev") return t("uncertain", lang);
    if (e.reason) return t("safePass", lang) + e.reason;
    return t("unrecorded", lang);
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

  function summarizeApplication(metrics) {
    metrics = metrics || {};
    return {
      selected: Number(metrics.selected) || 0,
      delivered: Number(metrics.delivered) || 0,
      started: Number(metrics.started) || 0,
      result_received: Number(metrics.result_received) || 0,
      verified: Number(metrics.verified) || 0,
      failed: Number(metrics.failed) || 0,
      unapplied: Number(metrics.unapplied) || 0,
      local_skip: Number(metrics.local_skip) || 0,
      missing_usage: Number(metrics.missing_usage) || 0,
      sample: !!metrics.sample
    };
  }

  function unappliedReasons(metrics) {
    const src = (metrics && metrics.unapplied_reason) || {};
    return Object.keys(src).sort().map(function (k) { return { reason: k, count: src[k] }; });
  }

  function formatEffect(kind) {
    switch (kind) {
      case "increase": return "増加";
      case "decrease": return "削減";
      case "unapplied": return "未適用";
      case "local_skip": return "正常なローカル省略";
      case "missing": return "欠測";
      case "none": return "比較なし";
      default: return "比較なし";
    }
  }

  function filterEvents(events, filters) {
    filters = filters || {};
    return (events || []).filter(function (e) {
      if (filters.host && e.host !== filters.host) return false;
      if (filters.source && e.source !== filters.source) return false;
      if (filters.apply && e.apply !== filters.apply) return false;
      return true;
    });
  }

  const CLASS_ORDER = ["model", "subagent", "skill", "mcp_tool", "cli", "plugin", "compaction"];

  function classLabel(kind, lang) {
    return t("class_" + kind, lang) || kind;
  }

  function classStatusLabel(status, lang) {
    return t("st_" + status, lang) || status;
  }

  function classMapFromPayload(payload, lang) {
    const metrics = (payload && payload.metrics) || {};
    if (Array.isArray(metrics.by_class) && metrics.by_class.length) {
      return metrics.by_class;
    }
    const apps = (payload && payload.applications) || [];
    const events = (payload && payload.events) || [];
    const modes = (payload && payload.router && payload.router.kindModes) || {};
    const by = {};
    CLASS_ORDER.forEach(function (k) { by[k] = { kind: k, label: classLabel(k, lang), status: "unobserved", count: 0, verified: 0, hosts: {}, evidence: "" }; });
    apps.forEach(function (a) {
      if (!a) return;
      const cell = by[a.kind];
      if (cell) {
        cell.count++;
        if (a.host) cell.hosts[a.host] = (cell.hosts[a.host] || 0) + 1;
        if (a.verified || a.state === "verified") cell.verified++;
        cell.status = a.state === "verified" ? "verified" : a.state === "delivered" ? "delivered" : a.state === "started" ? "started" : cell.status;
        cell.evidence = a.capabilityId || cell.evidence;
      }
      if (a.pluginOf && by.plugin) {
        by.plugin.count++;
        by.plugin.status = a.state === "verified" ? "verified" : by.plugin.status;
        by.plugin.evidence = a.pluginOf;
      }
    });
    events.forEach(function (e) {
      if (e.reasoningChanged || (e.originalModel && e.sentModel && e.originalModel !== e.sentModel)) {
        by.model.count++;
        by.model.status = "rewritten";
        by.model.evidence = e.reasoningChanged ? "effort" : e.originalModel + "→" + e.sentModel;
      }
      if (e.compactApplied) {
        by.compaction.count++;
        by.compaction.status = "rewritten";
      }
    });
    CLASS_ORDER.forEach(function (k) {
      if (by[k].count) return;
      const mode = modes[k];
      if (k === "compaction") return;
      if (mode === "off") by[k].status = "off";
      else if (mode === "apply") by[k].status = k === "model" ? "unsupported" : "unobserved";
      else by[k].status = "observe";
    });
    return CLASS_ORDER.map(function (k) { return by[k]; });
  }

  function filterApplications(apps, kind) {
    return (apps || []).filter(function (a) { return !kind || a.kind === kind || (kind === "plugin" && a.pluginOf); });
  }

  const SAMPLE_EVENTS = [
    { seq: 1, host: "grok", source: "local", apply: "filter", chosen: "read_file", reason: "local", confidence: 0.2, changed: true, toolsBefore: ["read_file", "exec"], toolsAfter: ["read_file"], usage: { inputTokens: 12, outputTokens: 3 } },
    { seq: 2, host: "codex", source: "jev", apply: "filter", chosen: "exec", reason: "top_set_jev", confidence: 0.7, changed: true, toolsBefore: ["exec", "web"], toolsAfter: ["exec"], usage: { inputTokens: 9, outputTokens: 4 } },
    { seq: 3, host: "cursor", source: "passthrough", apply: "none", chosen: "", reason: "unknown_history", changed: false, usageMissing: "no_usage" }
  ];

  const SAMPLE_METRICS = {
    selected: 2, delivered: 1, started: 1, result_received: 0, verified: 1,
    failed: 0, unapplied: 1, local_skip: 1, missing_usage: 1,
    unapplied_reason: { selected_not_delivered: 1 },
    by_kind: { skill: 1, cli: 1, mcp_tool: 1 },
    by_class: [
      { kind: "model", label: "モデルとeffort", status: "rewritten", count: 1, verified: 0, evidence: "effort" },
      { kind: "subagent", label: "子エージェント", status: "observe", count: 0, verified: 0 },
      { kind: "skill", label: "スキル", status: "delivered", count: 1, verified: 0, evidence: "skill:local:review@1" },
      { kind: "mcp_tool", label: "MCPツール", status: "verified", count: 1, verified: 1, evidence: "mcp_tool:host:lookup@1" },
      { kind: "cli", label: "CLI", status: "verified", count: 1, verified: 1, evidence: "cli:local:exec@1" },
      { kind: "plugin", label: "プラグイン", status: "unobserved", count: 0, verified: 0 },
      { kind: "compaction", label: "履歴圧縮", status: "rewritten", count: 1, verified: 0 }
    ],
    sample: true
  };

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
    const applyCounts = document.getElementById("apply-counts");
    const unappliedEl = document.getElementById("unapplied-chart");
    const effectEl = document.getElementById("effect-list");
    const sampleNote = document.getElementById("sample-note");
    const cmp = document.getElementById("cmp");
    const cmpOut = document.getElementById("cmp-out");
    const filterHost = document.getElementById("filter-host");
    const filterSource = document.getElementById("filter-source");
    const filterApply = document.getElementById("filter-apply");
    const reconnectBtn = document.getElementById("reconnect");
    const detail = document.getElementById("detail");
    const funnelEl = document.getElementById("funnel");
    const kindEl = document.getElementById("kind-map");
    const appRows = document.getElementById("app-rows");
    const filterKind = document.getElementById("filter-kind");
    const language = document.getElementById("language");
    if (!status || !rows) return;
    const useSample = typeof location !== "undefined" && /(?:\?|&)sample=1(?:&|$)/.test(location.search || "");
    let store = [];
    let since = 0;
    let lastPayload = null;
    let selectedSeq = 0;
    let lastUpdated = "";
    let connected = true;
    let lang = language && language.value === "en" ? "en" : "ja";

    function text(el, value) {
      el.textContent = value == null ? "" : String(value);
    }

    function renderOverview(summary) {
      countsEl.replaceChildren();
      overviewGroups(summary, lang).forEach(function (group) {
        const box = document.createElement("div");
        box.className = "count-group";
        const title = document.createElement("h3");
        const help = document.createElement("p");
        const values = document.createElement("dl");
        values.className = "count-values";
        text(title, group.title);
        text(help, group.help);
        group.values.forEach(function (entry) {
          const dt = document.createElement("dt");
          const dd = document.createElement("dd");
          text(dt, entry.label);
          text(dd, entry.value);
          if (entry.key === "jev_failed" && entry.value) dd.className = "danger";
          values.append(dt, dd);
        });
        box.append(title, help, values);
        countsEl.appendChild(box);
      });
    }

    function translatePage() {
      document.documentElement.lang = lang;
      document.querySelectorAll("[data-i18n]").forEach(function (el) { text(el, t(el.dataset.i18n, lang)); });
    }

    function currentFilters() {
      return {
        host: filterHost && filterHost.value,
        source: filterSource && filterSource.value,
        apply: filterApply && filterApply.value
      };
    }

    function fillHostFilter(events) {
      if (!filterHost) return;
      const prev = filterHost.value;
      const hosts = [];
      for (const e of events || []) {
        if (e.host && hosts.indexOf(e.host) < 0) hosts.push(e.host);
      }
      filterHost.replaceChildren();
      const all = document.createElement("option");
      all.value = "";
      text(all, t("all", lang));
      filterHost.appendChild(all);
      hosts.sort().forEach(function (h) {
        const opt = document.createElement("option");
        opt.value = h;
        text(opt, h);
        filterHost.appendChild(opt);
      });
      if (prev && hosts.indexOf(prev) >= 0) filterHost.value = prev;
    }

    function showDetail(e, apps) {
      if (!detail) return;
      if (!e) {
        detail.hidden = true;
        return;
      }
      detail.hidden = false;
      const related = (apps || []).filter(function (a) {
        return a && (String(a.decisionId) === String(e.seq) || (e.chosen && a.capabilityId && String(a.capabilityId).indexOf(e.chosen) >= 0));
      });
      const lines = [
        "連番 " + e.seq,
        "ホスト " + (e.host || "—"),
        "判定元 " + (e.source || "—"),
        "適用 " + (e.apply || "—"),
        "採用 " + (e.chosen || "—"),
        "理由 " + (e.reason || "—")
      ];
      if (related.length) {
        related.forEach(function (a) {
          lines.push("判断ID " + a.decisionId + " · " + a.kind + " · " + a.state + (a.callId ? " · 操作 " + a.callId : ""));
        });
      } else if (payloadApps().length) {
        payloadApps().slice(0, 5).forEach(function (a) {
          lines.push("判断ID " + a.decisionId + " · " + a.kind + " · " + a.state);
        });
      }
      text(detail, lines.join("\n"));
    }

    function payloadApps() {
      return (lastPayload && lastPayload.applications) || [];
    }

    function renderBars(el, rows) {
      if (!el) return;
      el.replaceChildren();
      const max = Math.max.apply(null, rows.map(function (r) { return r[1]; }).concat([1]));
      rows.forEach(function (row) {
        const item = document.createElement("div");
        item.className = "funnel-row";
        const name = document.createElement("span");
        const meter = document.createElement("i");
        const amount = document.createElement("b");
        text(name, row[0]);
        meter.style.width = (row[1] / max) * 100 + "%";
        text(amount, String(row[1]));
        item.append(name, meter, amount);
        el.appendChild(item);
      });
    }

    function renderClassMap(el, payload) {
      if (!el) return;
      el.replaceChildren();
      const cells = classMapFromPayload(payload, lang);
      const table = document.createElement("table");
      const head = document.createElement("thead");
      const hr = document.createElement("tr");
      ["処理", "状態", "件数", "成果確認", "根拠"].forEach(function (h) {
        const th = document.createElement("th");
        th.scope = "col";
        text(th, h);
        hr.appendChild(th);
      });
      head.appendChild(hr);
      const body = document.createElement("tbody");
      cells.forEach(function (c) {
        const tr = document.createElement("tr");
        const vals = [
          c.label || classLabel(c.kind, lang),
          classStatusLabel(c.status, lang),
          String(c.count || 0),
          String(c.verified || 0),
          c.evidence || "—"
        ];
        vals.forEach(function (v, i) {
          const td = document.createElement("td");
          text(td, v);
          if (i === 1) td.className = "status " + (c.status || "unobserved");
          tr.appendChild(td);
        });
        body.appendChild(tr);
      });
      table.append(head, body);
      el.appendChild(table);
    }

    function fillKindFilter(apps) {
      if (!filterKind) return;
      const prev = filterKind.value;
      const kinds = [];
      (apps || []).forEach(function (a) {
        if (a && a.kind && kinds.indexOf(a.kind) < 0) kinds.push(a.kind);
        if (a && a.pluginOf && kinds.indexOf("plugin") < 0) kinds.push("plugin");
      });
      filterKind.replaceChildren();
      const all = document.createElement("option");
      all.value = "";
      text(all, t("all", lang));
      filterKind.appendChild(all);
      kinds.sort().forEach(function (k) {
        const opt = document.createElement("option");
        opt.value = k;
        text(opt, classLabel(k, lang));
        filterKind.appendChild(opt);
      });
      if (prev && kinds.indexOf(prev) >= 0) filterKind.value = prev;
    }

    function renderApps(apps) {
      if (!appRows) return;
      fillKindFilter(apps);
      appRows.replaceChildren();
      const shown = filterApplications(apps, filterKind && filterKind.value);
      if (!shown.length) {
        const tr = document.createElement("tr");
        const td = document.createElement("td");
        td.colSpan = 5;
        text(td, "この実行では適用操作はまだありません。分類の状態は上の表を見てください。");
        tr.appendChild(td);
        appRows.appendChild(tr);
        return;
      }
      shown.forEach(function (a) {
        const tr = document.createElement("tr");
        [classLabel(a.kind, lang), a.state || "—", a.capabilityId || "—", a.callId || "—", a.host || "—"].forEach(function (v) {
          const td = document.createElement("td");
          text(td, v);
          tr.appendChild(td);
        });
        appRows.appendChild(tr);
      });
    }

    function render(payload) {
      if (!payload) {
      text(status, connected ? t("unavailable", lang) : t("disconnected", lang) + " · " + t("updated", lang) + " " + lastUpdated);
        return;
      }
      lastPayload = payload;
      const r = payload.router || {};
      const merged = mergeEvents(store, payload.events, r.oldestSeq, payload.historyTruncated);
      store = merged.events;
      if (store.length) since = store[store.length - 1].seq;
      fillHostFilter(store);
      const summary = summarizeEvents(store, r.counts);
      const totals = usageTotals(store);
      const unsupported = unknownHistoryDetails(store);
      const skipped = skippedTools(store);
      const app = summarizeApplication(payload.metrics);
      if (sampleNote) sampleNote.hidden = !app.sample;
      if (applyCounts) {
        applyCounts.replaceChildren();
        for (const [k, v] of Object.entries(app)) {
          if (k === "sample") continue;
          const dt = document.createElement("dt");
          text(dt, k);
          const dd = document.createElement("dd");
          text(dd, v);
          applyCounts.append(dt, dd);
        }
      }
      if (unappliedEl) {
        unappliedEl.replaceChildren();
        const reasons = unappliedReasons(payload.metrics);
        if (!reasons.length) text(unappliedEl, "未適用はありません");
        reasons.forEach(function (row) {
          const item = document.createElement("span");
          text(item, row.reason + " · " + row.count + " 件");
          unappliedEl.appendChild(item);
        });
      }
      if (effectEl) {
        effectEl.replaceChildren();
        const effects = [];
        if (app.local_skip) effects.push("local_skip");
        if (app.unapplied) effects.push("unapplied");
        if (app.missing_usage) effects.push("missing");
        if (!effects.length) effects.push("none");
        effects.forEach(function (kind) {
          const item = document.createElement("li");
          text(item, formatEffect(kind));
          effectEl.appendChild(item);
        });
      }
      lastUpdated = (r.now && String(r.now)) || new Date().toISOString();
      text(
        status,
        (connected ? "" : t("disconnected", lang) + " · ") +
          t("instance", lang) + " " +
          (r.instanceId || "?") +
          " · " + t("recorded", lang) + " " +
          (r.recorded != null ? r.recorded : store.length) +
          (payload.historyTruncated ? " · " + t("historyTruncated", lang) : "") +
          (r.mode ? " · " + t("mode", lang) + " " + r.mode : "") +
          " · " + t("updated", lang) + " " + lastUpdated +
          (useSample ? " · " + t("sample", lang) : "")
      );
      renderOverview(summary);
      if (usageEl) {
        const metrics = [[t("input", lang), totals.input, "input"], [t("output", lang), totals.output, "output"], [t("cacheRead", lang), totals.cached, "cached"], [t("cacheWrite", lang), totals.cacheWrite, "cached"], [t("reasoning", lang), totals.reasoning, "reasoning"], [t("directSaved", lang), totals.directSaved, "cached"], [t("compactSaved", lang), totals.compactionSaved, "cached"]];
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
          text(amount, value.toLocaleString() + " " + t("tokens", lang));
          item.append(name, meter, amount);
          usageEl.appendChild(item);
        }
        const note = document.createElement("small");
        text(note, totals.known + " " + t("usageNote", lang));
        usageEl.appendChild(note);
      }
      if (historyEl) {
        historyEl.replaceChildren();
        const entries = Object.entries(unsupported).sort((a, b) => b[1].count - a[1].count);
        if (!entries.length) {
          text(historyEl, "未対応の履歴形式はまだ観測されていません");
        } else {
          for (const [shape, detail] of entries) {
            const item = document.createElement("details");
            item.className = "history-chip";
            const summary = document.createElement("summary");
            text(summary, shape + " · " + detail.count + " 件");
            const requests = document.createElement("div");
            text(requests, "該当リクエスト: " + detail.requests.join(", "));
            item.append(summary, requests);
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
      renderBars(funnelEl, [
        ["選定", app.selected],
        ["配達", app.delivered],
        ["開始", app.started],
        ["結果受信", app.result_received],
        ["成果確認", app.verified]
      ]);
      renderClassMap(kindEl, payload);
      renderApps(payloadApps());
      rows.replaceChildren();
      const shown = filterEvents(store, currentFilters()).slice(-TABLE_ROWS).reverse();
      if (selectedSeq && !shown.some(function (e) { return e.seq === selectedSeq; })) selectedSeq = shown[0] ? shown[0].seq : 0;
      for (const e of shown) {
        const tr = document.createElement("tr");
        tr.dataset.seq = String(e.seq);
        tr.tabIndex = 0;
        if (e.seq === selectedSeq) tr.className = "selected";
        const usage = formatUsage(e.usage, e.usageMissing, e.usagePartial);
        const savings = formatSavings(e.savedTokens);
        const vals = [
          e.seq,
          e.host || "—",
          e.source,
          e.apply,
          routeOutcome(e, lang),
          e.chosen,
          e.reason,
          formatConfidence(e.confidence),
          e.changed ? t("yes", lang) : t("no", lang),
          toolReplacement(e),
          savings ? usage.text + " · " + savings : usage.text,
          e.headerMs != null ? e.headerMs + "ms" : e.bodyMs != null ? e.bodyMs + "ms" : t("missing", lang),
        ];
        for (const v of vals) {
          const td = document.createElement("td");
          text(td, v);
          if (usage.missing && !savings && v === usage.text) td.className = "missing";
          tr.appendChild(td);
        }
        tr.addEventListener("click", function () {
          selectedSeq = e.seq;
          showDetail(e, payloadApps());
          render(lastPayload);
        });
        rows.appendChild(tr);
      }
      const selected = shown.filter(function (e) { return e.seq === selectedSeq; })[0];
      if (selected) showDetail(selected, payloadApps());
    }

    function reconnect() {
      store = [];
      since = 0;
      selectedSeq = 0;
      lastPayload = null;
      connected = true;
      if (filterHost) filterHost.value = "";
      if (filterSource) filterSource.value = "";
      if (filterApply) filterApply.value = "";
      if (filterKind) filterKind.value = "";
      if (detail) detail.hidden = true;
      poll();
    }

    async function poll() {
      if (useSample) {
        connected = true;
        render({
          router: { instanceId: "sample", recorded: SAMPLE_EVENTS.length, now: new Date().toISOString(), mode: "sample" },
          events: SAMPLE_EVENTS,
          metrics: SAMPLE_METRICS,
          applications: [
            { decisionId: "dec-sample", state: "verified", kind: "cli", capabilityId: "cli:local:exec@1", callId: "call_sample", verified: true, host: "codex" },
            { decisionId: "dec-skill", state: "delivered", kind: "skill", capabilityId: "skill:local:review@1", deliveredHash: "abc", host: "grok" },
            { decisionId: "dec-mcp", state: "verified", kind: "mcp_tool", capabilityId: "mcp_tool:host:lookup@1", callId: "call_mcp", verified: true, host: "grok" },
            { decisionId: "dec-model", state: "verified", kind: "model", capabilityId: "model:claude:claude-opus-5@medium", callId: "jev", verified: true, host: "claude" }
          ],
          historyTruncated: false
        });
        return;
      }
      try {
        const res = await fetch("/dashboard/events?since=" + encodeURIComponent(since), {
          headers: { accept: "application/json" },
        });
        if (!res.ok) {
          connected = false;
          text(status, t("disconnected", lang) + " · " + t("updated", lang) + " " + (lastUpdated || t("no", lang)) + " (" + res.status + ")");
          return;
        }
        connected = true;
        render(await res.json());
      } catch (err) {
        connected = false;
        text(status, t("disconnected", lang) + " · " + t("updated", lang) + " " + (lastUpdated || t("no", lang)));
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
    [filterHost, filterSource, filterApply, filterKind].forEach(function (el) {
      if (!el) return;
      el.addEventListener("change", function () {
        if (lastPayload) render(Object.assign({}, lastPayload, { events: [] }));
      });
    });
    if (reconnectBtn) reconnectBtn.addEventListener("click", function () {
      reconnect();
      reconnectBtn.blur();
    });
    if (language) language.addEventListener("change", function () {
      lang = language.value === "en" ? "en" : "ja";
      translatePage();
      if (lastPayload) render(Object.assign({}, lastPayload, { events: [] }));
    });
    document.addEventListener("keydown", function (ev) {
      const tag = ev.target && ev.target.tagName;
      if (tag === "TEXTAREA" || tag === "INPUT" || tag === "SELECT" || tag === "BUTTON") return;
      const shown = filterEvents(store, currentFilters()).slice(-TABLE_ROWS).reverse();
      if (ev.key === "j" || ev.key === "k") {
        if (!shown.length) return;
        ev.preventDefault();
        let idx = shown.findIndex(function (e) { return e.seq === selectedSeq; });
        if (idx < 0) idx = 0;
        else idx += ev.key === "j" ? 1 : -1;
        if (idx < 0) idx = 0;
        if (idx >= shown.length) idx = shown.length - 1;
        selectedSeq = shown[idx].seq;
        if (lastPayload) render(Object.assign({}, lastPayload, { events: [] }));
      } else if (ev.key === "Enter") {
        const selected = shown.filter(function (e) { return e.seq === selectedSeq; })[0] || shown[0];
        if (selected) {
          selectedSeq = selected.seq;
          showDetail(selected, payloadApps());
        }
      } else if (ev.key === "r") {
        reconnect();
      }
    });
    translatePage();
    poll();
    setInterval(poll, 2000);
  }

  return { clip, formatUsage, formatSavings, usageTotals, toolReplacement, summarizeUnsupportedHistory, unknownHistoryDetails, formatConfidence, routeOutcome, skippedTools, summarizeEvents, overviewGroups, t, formatComparison, mergeEvents, summarizeApplication, unappliedReasons, formatEffect, filterEvents, classMapFromPayload, classLabel, classStatusLabel, filterApplications, SAMPLE_EVENTS, start };
});
