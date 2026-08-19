/* ============================================================
   设计云平台 (Design Cloud Platform) — 运行态势感知大屏 JS
   数据驱动与渲染逻辑 (Clean & Streamlined Frontend Engine)
   ============================================================ */

const PUBLIC_DASHBOARD_TOKEN = "__dc_public_dashboard_guest_token__";
const VERSION_KEY = "dashVersion";

let state = {};
try {
  const cached = localStorage.getItem("dc_dashboard_state");
  if (cached) {
    state = JSON.parse(cached);
  }
} catch (e) {
  console.warn("Failed to load state from localStorage", e);
}

const $ = id => document.getElementById(id);

const fmt = n => {
  const num = Number(n);
  if (isNaN(num)) return "--";
  return Number.isInteger(num)
    ? num.toLocaleString("zh-CN")
    : num.toLocaleString("zh-CN", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
};

function setRing(ringId, textId, ratio) {
  const len = 163.4;
  const el = $(ringId);
  if (el) el.style.strokeDashoffset = len * (1 - Math.min(1, Math.max(0, ratio)));
  if (textId) {
    const txt = $(textId);
    if (txt) txt.textContent = (ratio * 100).toFixed(2) + "%";
  }
}

/* ---------- 告警视觉阈值：来自 DomainAlert 配置（0=未启用回退内置默认） ---------- */
function resolveAlertThresholds(alertCfg) {
  const a = alertCfg || {};
  const storageRed = (a.storageUsedPercent && a.storageUsedPercent > 0) ? a.storageUsedPercent : 90;
  const loadBusy = (a.loadRatio && a.loadRatio > 0) ? a.loadRatio : 0.7;
  return {
    storageRed,
    storageAmber: storageRed - 15,
    loadBusy,
    loadOverload: loadBusy + 0.3,
  };
}

/* ---------- 系统负载 1m/5m/15m 按核心归一化动态着色与进度条渲染 ---------- */
function renderLoadNumbers(load1m, load5m, load15m, onlineCores, busyRatio = 0.7, overloadRatio = 1.0) {
  const cores = onlineCores || 1;
  if ($("loadBaseCores")) $("loadBaseCores").textContent = fmt(cores);

  const updateRow = (val, valElId, barElId, pctElId) => {
    const valEl = $(valElId);
    const barEl = $(barElId);
    const pctEl = $(pctElId);
    const numVal = Number(val) || 0;
    const ratio = numVal / cores;

    let colorClass = "load-normal";
    let bgClass = "load-normal-bg";
    if (ratio > overloadRatio) {
      colorClass = "load-overload";
      bgClass = "load-overload-bg";
    } else if (ratio > busyRatio) {
      colorClass = "load-busy";
      bgClass = "load-busy-bg";
    }

    if (valEl) {
      valEl.textContent = val ?? "0.0";
      valEl.className = `val-num ${colorClass}`;
    }
    if (barEl) {
      const pctVal = Math.min(100, (numVal * 100) / cores);
      barEl.style.width = pctVal.toFixed(2) + "%";
      barEl.className = bgClass;
    }
    if (pctEl) {
      const rawPct = (numVal * 100) / cores;
      pctEl.textContent = rawPct.toFixed(2) + "%";
      pctEl.className = `load-bar-pct ${colorClass}`;
    }
  };

  updateRow(load1m, "kpiLoad1", "barLoad1", "pctLoad1");
  updateRow(load5m, "kpiLoad5", "barLoad5", "pctLoad5");
  updateRow(load15m, "kpiLoad15", "barLoad15", "pctLoad15");
}

/* ---------- HPC 队列 Table ---------- */
function renderQueueTable(queuesData) {
  const tbody = $("queueTableBody");
  if (!tbody) return;

  const queueList = queuesData || state.scheduler?.queues || [];
  if (queueList.length === 0) {
    tbody.innerHTML = `<tr><td colspan="7" class="text-muted" style="text-align:center; padding:16px;">暂无队列作业数据</td></tr>`;
    return;
  }

  tbody.innerHTML = queueList.map(q => {
    const allocated = q.allocated ?? 0;
    const run = q.running ?? 0;
    const pend = q.pending ?? 0;
    const susp = q.suspended ?? 0;
    const done = q.completed ?? 0;
    const fail = q.failed ?? 0;
    const failColor = typeof fail === "number" && fail > 0 ? "var(--accent-rose)" : "var(--text-muted)";

    return `
    <tr>
      <td><span class="q-name-badge">${q.name}</span></td>
      <td><b class="text-mono text-bold" style="color:var(--text-main); font-size:13px;">${fmt(allocated)}</b></td>
      <td><b class="text-mono text-bold" style="color:var(--primary-cyan);">${fmt(run)}</b></td>
      <td><b class="text-mono text-bold" style="color:var(--accent-amber);">${fmt(pend)}</b></td>
      <td><b class="text-mono text-bold" style="color:var(--accent-emerald);">${fmt(done)}</b></td>
      <td class="text-mono text-muted">${fmt(susp)}</td>
      <td class="text-mono" style="color:${failColor};">${fmt(fail)}</td>
    </tr>`;
  }).join("");
}

/* ---------- 存储 Donut 饼图 ---------- */
function renderStorageDonut(storage, redPct = 90, amberPct = 75) {
  if (!storage || !storage.summary) return;
  const sum = storage.summary;

  const ratio = sum.utilization || 0;
  const pctVal = ratio * 100;
  const pctStr = pctVal.toFixed(2);

  let strokeColor = "var(--accent-emerald)";
  if (pctVal > redPct) {
    strokeColor = "var(--accent-rose)";
  } else if (pctVal > amberPct) {
    strokeColor = "var(--accent-amber)";
  }

  const arc = $("storageDonutArc");
  if (arc) {
    arc.style.strokeDasharray = "339.3";
    arc.style.strokeDashoffset = 339.3 * (1 - ratio);
    arc.style.stroke = strokeColor;
  }
  const pctTxt = $("donutPctText");
  if (pctTxt) {
    pctTxt.textContent = pctStr + "%";
    pctTxt.setAttribute("fill", strokeColor);
  }

  if ($("storageUsedVal")) {
    $("storageUsedVal").textContent = (sum.used ?? "--") + " " + (sum.unit || "TB");
    $("storageUsedVal").style.color = strokeColor;
  }
  if ($("storageTotalVal")) {
    $("storageTotalVal").textContent = (sum.total ?? "--") + " " + (sum.unit || "TB");
  }
}

/* ---------- 存储详情表 ---------- */
function renderTable(storage, redPct = 90, amberPct = 75) {
  const tbody = $("storageTableBody");
  if (!tbody || !storage || !storage.items) return;

  tbody.innerHTML = storage.items.map(it => {
    let barClass = "";
    let pctColor = "var(--text-main)";
    const ratio = it.total > 0 ? it.used / it.total : 0;
    const pctVal = ratio * 100;
    const pctStr = pctVal.toFixed(2);

    if (pctVal > redPct) {
      barClass = "danger";
      pctColor = "var(--accent-rose)";
    } else if (pctVal > amberPct) {
      barClass = "warn";
      pctColor = "var(--accent-amber)";
    } else {
      pctColor = "var(--accent-emerald)";
    }

    return `
    <tr>
      <td class="text-mono text-muted">${it.id}</td>
      <td class="text-bold">${it.name}</td>
      <td><span class="badge-type ${it.type}">${(it.type || "").toUpperCase()}</span></td>
      <td>${fmt(it.total)} ${it.unit || "GB"}</td>
      <td class="text-bold" style="color:${pctColor}">${fmt(it.used)} ${it.unit || "GB"}</td>
      <td>
        <div class="table-bar-box">
          <div class="table-bar ${barClass}">
            <i style="width:${Math.min(100, pctVal)}%"></i>
          </div>
          <span class="text-mono" style="color:${pctColor}">${pctStr}%</span>
        </div>
      </td>
      <td style="font-size:11px; white-space:nowrap;">${renderTimeHTML(it.sampled_at)}</td>
    </tr>`;
  }).join("");
}

/* ---------- 作业比例条 ---------- */
function renderJobBar(jobs) {
  if (!jobs) return;
  const run = jobs.running || 0;
  const pend = jobs.pending || 0;
  const susp = jobs.suspended || 0;
  const done = jobs.completed || 0;
  const fail = jobs.failed || 0;
  const total = (run + pend + susp + done + fail) || 1;

  const bar = $("jobBar");
  if (!bar) return;

  bar.innerHTML = `
    <i class="run" style="width:${(run / total) * 100}%" title="运行中: ${run}"></i>
    <i class="pend" style="width:${(pend / total) * 100}%" title="等待中: ${pend}"></i>
    <i class="susp" style="width:${(susp / total) * 100}%" title="已暂停: ${susp}"></i>
    <i class="done" style="width:${(done / total) * 100}%" title="已完成: ${done}"></i>
    <i class="fail" style="width:${(fail / total) * 100}%" title="已失败: ${fail}"></i>
  `;
}

function animateNum(el, to) {
  if (!el) return;
  const numTo = Number(to) || 0;
  const strTo = String(to ?? "0");
  const dotIdx = strTo.indexOf(".");
  const decimals = dotIdx >= 0 ? (strTo.length - dotIdx - 1) : 0;

  const from = parseFloat(el.dataset.v || "0") || 0;
  const dur = 500, t0 = performance.now();
  el.dataset.v = numTo;

  const step = now => {
    const k = Math.min(1, (now - t0) / dur);
    const eased = 1 - Math.pow(1 - k, 3);
    const val = from + (numTo - from) * eased;
    el.textContent = decimals > 0 ? val.toFixed(decimals) : Math.round(val).toLocaleString("zh-CN");
    if (k < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

/* ---------- 时间解析与状态呈现 ---------- */
function isStaleTime(ts) {
  if (!ts) return false;
  const t = new Date(ts);
  if (isNaN(t.getTime())) return false;
  return (Date.now() - t.getTime()) > (24 * 60 * 60 * 1000);
}

function fmtTime(ts) {
  if (!ts) return "--";
  const t = new Date(ts);
  if (isNaN(t.getTime())) return "--";
  return t.toLocaleString("zh-CN", {
    month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false
  });
}

function renderTimeHTML(ts) {
  if (!ts) return '<span class="text-muted">--</span>';
  const formatted = fmtTime(ts);
  if (isStaleTime(ts)) {
    return `<span class="text-rose text-bold" title="更新时间超过24小时">${formatted}</span>`;
  }
  return `<span class="text-muted">${formatted}</span>`;
}

function updateDataTimestamps(d) {
  const upd = $("dataUpdated");
  if (!upd) return;
  upd.textContent = "更新于 " + fmtTime(d.sampled_at);
  if (isStaleTime(d.sampled_at)) {
    upd.style.color = "var(--accent-rose)";
    upd.style.fontWeight = "600";
    upd.title = "整体采样更新时间超过24小时";
  } else {
    upd.style.color = "";
    upd.style.fontWeight = "";
    upd.title = "";
  }
}

/* ---------- 整屏数据渲染主入口 ---------- */
function render(d) {
  if (!d || Object.keys(d).length === 0) return;
  try {
    localStorage.setItem("dc_dashboard_state", JSON.stringify(d));
  } catch (e) { }

  updateDataTimestamps(d);

  const hasGpu = Boolean(d.gpu?.cards?.configured > 0);
  const topCardsSection = $("topCardsSection");

  if (hasGpu) {
    topCardsSection.classList.remove("no-gpu");
    const gpuNodes = d.gpu.nodes || { online: 0, configured: 0 };
    const gpuCards = d.gpu.cards || { online: 0, configured: 0, utilization: 0 };
    const gpuVram = d.gpu.vram || { online: 0, configured: 0, used: 0, unit: "GB" };

    if ($("kpiGpuNodesOnline")) animateNum($("kpiGpuNodesOnline"), gpuNodes.online);
    if ($("kpiGpuNodesTotal")) $("kpiGpuNodesTotal").textContent = "/" + gpuNodes.configured;
    if (gpuNodes.configured > 0) setRing("ringGpuNodes", "ringGpuNodesText", gpuNodes.online / gpuNodes.configured);

    if ($("kpiGpuOnline")) animateNum($("kpiGpuOnline"), gpuCards.online);
    if ($("kpiGpuTotal")) $("kpiGpuTotal").textContent = "/" + gpuCards.configured;

    const gpuUtil = gpuCards.utilization || 0;
    if ($("kpiGpu")) animateNum($("kpiGpu"), (gpuUtil * 100).toFixed(2));
    if (gpuCards.utilization !== undefined) setRing("ringGpu", "ringGpuText", gpuUtil);

    if ($("kpiGpuMemOnlineStatic")) animateNum($("kpiGpuMemOnlineStatic"), gpuVram.online);
    if ($("kpiGpuMemTotalStatic")) $("kpiGpuMemTotalStatic").textContent = "/" + gpuVram.configured;
    if ($("kpiGpuMemStaticUnit")) $("kpiGpuMemStaticUnit").textContent = gpuVram.unit || "GB";

    if ($("kpiGpuMemUsed")) animateNum($("kpiGpuMemUsed"), gpuVram.used);
    if ($("kpiGpuMemTotal")) $("kpiGpuMemTotal").textContent = "/" + gpuVram.online;
    if ($("kpiGpuMemUnit")) $("kpiGpuMemUnit").textContent = gpuVram.unit || "GB";
    if (gpuVram.online > 0) setRing("ringGpuMem", "ringGpuMemText", gpuVram.used / gpuVram.online);
  } else {
    topCardsSection.classList.add("no-gpu");
  }

  const cpuConfigured = d.cpu ? d.cpu.configured : 1;
  const onlineCores = d.cpu?.online || 1;
  const alertTh = resolveAlertThresholds(d.alert);

  if (d.nodes) {
    const nodesConfigured = d.nodes.configured || 1;
    setRing("ringNodes", "ringNodesText", d.nodes.online / nodesConfigured);
    animateNum($("kpiNodes"), d.nodes.online);
    if ($("kpiNodesTotal")) $("kpiNodesTotal").textContent = "/" + nodesConfigured;
  }

  if (d.cpu) {
    const cpuUtil = d.cpu.utilization || 0;
    animateNum($("kpiCpu"), (cpuUtil * 100).toFixed(2));
    animateNum($("kpiCpuOnline"), d.cpu.online);
    if ($("kpiCpuTotal")) $("kpiCpuTotal").textContent = "/" + cpuConfigured;
    if (d.cpu.utilization !== undefined) setRing("ringCpuUtil", "ringCpuUtilText", cpuUtil);
  }

  if (d.load) {
    renderLoadNumbers(d.load["1m"], d.load["5m"], d.load["15m"], onlineCores, alertTh.loadBusy, alertTh.loadOverload);
  }

  if (d.memory) {
    const memConfigured = d.memory.configured;
    const memOnline = d.memory.online;
    if ($("kpiMemOnlineStatic")) animateNum($("kpiMemOnlineStatic"), memOnline);
    if ($("kpiMemTotalStatic")) $("kpiMemTotalStatic").textContent = "/" + memConfigured;
    if ($("kpiMemStaticUnit")) $("kpiMemStaticUnit").textContent = d.memory.unit || "GB";

    if ($("kpiMemUsedVal")) animateNum($("kpiMemUsedVal"), d.memory.used);
    if ($("kpiMemTotalVal")) $("kpiMemTotalVal").textContent = "/" + memOnline;
    if ($("kpiMemUsedUnit")) $("kpiMemUsedUnit").textContent = d.memory.unit || "GB";
    if (memOnline > 0) setRing("ringMemUsed", "ringMemUsedText", d.memory.used / memOnline);
  }

  const sched = (d.scheduler && d.scheduler.hosts) || { online: 0, total: 0 };
  $("schedNodesTag").innerHTML = `<span class="text-emerald text-bold text-mono">${sched.online}</span><span style="color:var(--primary-blue);" class="text-bold text-mono">/${sched.total}</span>`;
  setRing("ringSchedNodes", null, sched.total > 0 ? sched.online / sched.total : 0);

  renderStorageDonut(d.storage, alertTh.storageRed, alertTh.storageAmber);
  renderQueueTable(d.scheduler?.queues);
  renderTable(d.storage, alertTh.storageRed, alertTh.storageAmber);
  renderJobBar(d.scheduler?.jobs);

  if (d.scheduler?.jobs) {
    animateNum($("jobRunning"), d.scheduler.jobs.running);
    animateNum($("jobPending"), d.scheduler.jobs.pending);
    animateNum($("jobSuspended"), d.scheduler.jobs.suspended);
    animateNum($("jobDone"), d.scheduler.jobs.completed);
    animateNum($("jobFailed"), d.scheduler.jobs.failed);
  }
}

function tickClock() {
  const el = $("sysClock");
  if (el) el.textContent = new Date().toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  });
}

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    document.documentElement.requestFullscreen?.();
  } else {
    document.exitFullscreen?.();
  }
}

function setConnectionState(mode) {
  const connBadge = $("connBadge");
  const connText = $("connText");

  if (mode === "offline") {
    if (connBadge) connBadge.className = "conn-status offline";
    if (connText) connText.textContent = "网络断开";
  } else {
    if (connBadge) connBadge.className = "conn-status";
    if (connText) connText.textContent = "数据流正常";
  }
}

let sseSource = null;
let retryCount = 0;
let reconnectTimeoutId = null;
const MAX_RETRY_INTERVAL = 60000;

function scheduleReconnect(reconnectFn) {
  if (reconnectTimeoutId) clearTimeout(reconnectTimeoutId);

  let interval = 3000 * Math.pow(1.5, retryCount);
  if (interval > MAX_RETRY_INTERVAL) interval = MAX_RETRY_INTERVAL;

  const jitter = interval * 0.2;
  interval = interval - jitter + (Math.random() * (jitter * 2));

  reconnectTimeoutId = setTimeout(() => {
    retryCount++;
    reconnectFn();
  }, interval);
}

function initSSE() {
  if (!window.EventSource) return;
  if (sseSource) sseSource.close();

  const sseUrl = `/api/dashboard?stream=true&token=${encodeURIComponent(PUBLIC_DASHBOARD_TOKEN)}`;
  try {
    sseSource = new EventSource(sseUrl);
    sseSource.addEventListener("dashboard", function (event) {
      try {
        const json = JSON.parse(event.data);
        const ver = json.version || "";
        if (ver) {
          const last = sessionStorage.getItem(VERSION_KEY);
          if (last && last !== ver) {
            sessionStorage.setItem(VERSION_KEY, ver);
            location.reload();
            return;
          }
          sessionStorage.setItem(VERSION_KEY, ver);
        }
        const data = json.data || json;
        render(data);
        setConnectionState("online");
        retryCount = 0;
      } catch (e) {
        console.warn("SSE JSON parse error", e);
      }
    });

    sseSource.onerror = function () {
      if (sseSource) sseSource.close();
      setConnectionState("offline");
      scheduleReconnect(initSSE);
    };
  } catch (e) {
    scheduleReconnect(initSSE);
  }
}

/* ---------- 大屏版本轮询 2 小时兜底 ---------- */
(function initVersionChecker() {
  const FETCH_INTERVAL = 2 * 60 * 60 * 1000;

  async function checkVersion() {
    try {
      const res = await fetch("/api/version");
      if (!res.ok) return;
      const json = await res.json();
      const data = json.data || {};
      const current = data.version || "";
      if (!current) return;

      const last = sessionStorage.getItem(VERSION_KEY);
      if (last && last !== current) {
        sessionStorage.setItem(VERSION_KEY, current);
        location.reload();
        return;
      }
      sessionStorage.setItem(VERSION_KEY, current);
    } catch (e) { }
  }

  setInterval(checkVersion, FETCH_INTERVAL);
})();

/* ---------- 网页就绪初始化 ---------- */
render(state);
initSSE();
setInterval(tickClock, 1000);
tickClock();
