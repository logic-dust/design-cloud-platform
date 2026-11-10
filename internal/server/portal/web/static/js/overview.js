/* ============================================================
   设计云平台 (Design Cloud Platform) — 运行态势感知大屏 JS
   数据驱动与渲染逻辑 (Clean & Streamlined Frontend Engine)
   ============================================================ */

/* IIFE 封装：内部声明不泄漏为 window 属性，交互统一经 addEventListener 绑定 */
(function () {

  const PUBLIC_OVERVIEW_TOKEN = "__dc_public_overview_guest_token__";
  const VERSION_KEY = "dashVersion";
  const QUEUE_AUTO_SCROLL_INTERVAL = 4000;

  let state = {};
  let queueAutoScrollTimer = null;
  let queueAutoScrollPausedUntil = 0;
  try {
    const cached = localStorage.getItem("dc_overview_state");
    if (cached) {
      state = JSON.parse(cached);
    }
  } catch (e) {
    console.warn("Failed to load state from localStorage", e);
  }

  const $ = id => document.getElementById(id);

  const escapeHTML = globalThis.dcPortalEscapeHTML || (value => String(value ?? ""));

  const fmt = n => {
    const num = Number(n);
    if (isNaN(num)) return "--";
    return Number.isInteger(num)
      ? num.toLocaleString("zh-CN")
      : num.toLocaleString("zh-CN", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  };

  // Desktop NumberFormat.Quantity 对齐：>= 100 取整，< 100 保留 1 位小数
  const fmtQuantity = n => {
    const num = Number(n);
    if (isNaN(num)) return "--";
    return num >= 100 ? Math.round(num).toLocaleString("zh-CN") : num.toFixed(1);
  };

  // Desktop NumberFormat.Count 对齐：整数千分位
  const fmtCount = n => {
    const num = Number(n);
    if (isNaN(num)) return "--";
    return Math.round(num).toLocaleString("zh-CN");
  };

  /** 作业调度计数展示上限（与圆心、图例列宽 5.5ch / 「9999+」对齐） */
  const JOB_COUNT_DISPLAY_MAX = 9999;
  const fmtJobCountDisplay = n => {
    const num = Number(n);
    if (isNaN(num)) return "--";
    const rounded = Math.round(num);
    return rounded > JOB_COUNT_DISPLAY_MAX ? "9999+" : rounded.toLocaleString("zh-CN");
  };

  function applyJobCountTitle(el, raw) {
    if (!el) return;
    const rounded = Math.round(Number(raw) || 0);
    el.title = rounded > JOB_COUNT_DISPLAY_MAX ? fmtCount(rounded) : "";
  }

  function setRing(ringId, textId, ratio, decimals = 1) {
    const el = $(ringId);
    if (el) {
      // 周长读取元素自身的 stroke-dasharray，兼容不同半径的环（如 163.4 / 150.8）
      const len = parseFloat(el.getAttribute("stroke-dasharray")) || 0;
      if (len > 0) el.style.strokeDashoffset = len * (1 - Math.min(1, Math.max(0, ratio)));
    }
    if (textId) {
      const txt = $(textId);
      // 派生百分比与 Desktop NumberFormat.Percent 对齐：默认一位小数
      if (txt) txt.textContent = (ratio * 100).toFixed(decimals) + "%";
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

  /* ---------- 系统负载 1m/5m/15m：只展示原始负载，条长按在线核归一化（对齐 Desktop） ---------- */
  function renderLoadNumbers(load1m, load5m, load15m, onlineCores, busyRatio = 0.7, overloadRatio = 1.0) {
    const cores = onlineCores || 1;
    const note = $("loadCoresNote");
    if (note) {
      note.innerHTML = `* 仅统计在线节点，当前满载为 <span class="text-bold text-mono">${fmt(cores)}</span>`;
    }

    const updateCol = (val, valElId, barElId, colElId) => {
      const valEl = $(valElId);
      const barEl = $(barElId);
      const colEl = $(colElId);
      const numVal = Number(val) || 0;
      const ratio = numVal / cores;
      const pctVal = Math.min(100, ratio * 100);
      const pctText = (ratio * 100).toFixed(1) + "%";

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
        // 原始负载两位小数，与 Desktop NumberFormat.LoadConverter 一致
        valEl.textContent = numVal.toFixed(2);
        valEl.className = `val-num ${colorClass}`;
      }
      if (barEl) {
        barEl.style.setProperty("--bar-pct", pctVal.toFixed(2) + "%");
        barEl.className = bgClass;
      }
      if (colEl) {
        colEl.title = `${numVal.toFixed(2)} / ${fmt(cores)} · ${pctText}`;
      }
    };

    updateCol(load1m, "kpiLoad1", "barLoad1", "loadCol1");
    updateCol(load5m, "kpiLoad5", "barLoad5", "loadCol5");
    updateCol(load15m, "kpiLoad15", "barLoad15", "loadCol15");
  }

  function storageUtilTone(pctVal, redPct, amberPct) {
    if (pctVal > redPct) {
      return { barClass: "danger", textClass: "text-rose" };
    }
    if (pctVal > amberPct) {
      return { barClass: "warn", textClass: "text-amber" };
    }
    return { barClass: "", textClass: "text-emerald" };
  }

  /* ---------- HPC 队列 Table ---------- */
  function renderQueueTable(queuesData) {
    const tbody = $("queueTableBody");
    if (!tbody) return;

    const queueList = queuesData || state.scheduler?.queues || [];
    const queues = queueList.map(q => ({
      name: q.QUEUE_NAME ?? q.name ?? "-",
      njobs: Number(q.NJOBS ?? q.allocated ?? q.total_nodes ?? 0),
      run: q.RUN ?? q.running ?? 0,
      pend: q.PEND ?? q.pending ?? 0,
      done: q.DONE ?? q.completed ?? 0,
      susp: q.SUSP ?? q.suspended ?? 0,
      fail: q.FAIL ?? q.failed ?? 0,
    })).filter(q => Number.isFinite(q.njobs) && q.njobs > 0);

    if (queues.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" class="text-muted queue-table-empty">暂无已分配作业的队列</td></tr>`;
      initQueueAutoScroll();
      return;
    }

    tbody.innerHTML = queues.map(q => {
      return `
      <tr>
        <td><span class="q-name-badge">${escapeHTML(q.name)}</span></td>
        <td><b class="text-mono text-bold text-main queue-metric-alloc">${fmtCount(q.njobs)}</b></td>
        <td><b class="text-mono text-bold text-emerald">${fmtCount(q.run)}</b></td>
        <td><b class="text-mono text-bold text-cyan">${fmtCount(q.pend)}</b></td>
        <td class="text-mono text-muted">${fmtCount(q.done)}</td>
        <td class="text-mono text-amber">${fmtCount(q.susp)}</td>
        <td class="text-mono text-rose">${fmtCount(q.fail)}</td>
      </tr>`;
    }).join("");
    initQueueAutoScroll();
  }

  function advanceQueueTable() {
    const wrapper = $("queueTableBody")?.closest(".table-wrapper");
    if (!wrapper || document.hidden || Date.now() < queueAutoScrollPausedUntil) return;

    const maxScroll = wrapper.scrollHeight - wrapper.clientHeight;
    if (maxScroll <= 1) return;

    const rowHeight = wrapper.querySelector("tbody tr")?.getBoundingClientRect().height || 0;
    if (rowHeight <= 0) return;

    const nextTop = wrapper.scrollTop >= maxScroll - 1
      ? 0
      : Math.min(wrapper.scrollTop + rowHeight, maxScroll);
    wrapper.scrollTo({ top: nextTop, behavior: "smooth" });
  }

  function initQueueAutoScroll() {
    const wrapper = $("queueTableBody")?.closest(".table-wrapper");
    if (!wrapper) return;

    if (wrapper.dataset.autoScrollInitialized !== "true") {
      const pauseForUserInput = () => {
        queueAutoScrollPausedUntil = Date.now() + QUEUE_AUTO_SCROLL_INTERVAL * 2;
      };
      wrapper.addEventListener("wheel", pauseForUserInput, { passive: true });
      wrapper.addEventListener("pointerdown", pauseForUserInput);
      wrapper.dataset.autoScrollInitialized = "true";
    }

    if (queueAutoScrollTimer === null) {
      queueAutoScrollTimer = window.setInterval(advanceQueueTable, QUEUE_AUTO_SCROLL_INTERVAL);
    }
  }

  /* ---------- 存储 Donut 饼图 ---------- */
  function renderStorageDonut(storage, redPct = 90, amberPct = 75) {
    if (!storage || !storage.summary) return;
    const sum = storage.summary;

    const ratio = sum.utilization || 0;
    const pctVal = ratio * 100;
    const pctStr = pctVal.toFixed(1);

    let strokeColor = "var(--accent-emerald)";
    if (pctVal > redPct) {
      strokeColor = "var(--accent-rose)";
    } else if (pctVal > amberPct) {
      strokeColor = "var(--accent-amber)";
    }

    const arc = $("storageDonutArc");
    if (arc) {
      const len = parseFloat(arc.getAttribute("stroke-dasharray")) || 0;
      if (len > 0) arc.style.strokeDashoffset = len * (1 - ratio);
      arc.style.stroke = strokeColor;
    }
    const pctTxt = $("donutPctText");
    if (pctTxt) {
      pctTxt.textContent = pctStr + "%";
      pctTxt.setAttribute("fill", strokeColor);
    }

    const unit = sum.unit || "TB";
    if ($("storageUsedVal")) {
      $("storageUsedVal").textContent = (sum.used != null ? fmtQuantity(sum.used) : "--") + " " + unit;
      $("storageUsedVal").style.color = strokeColor;
    }
    if ($("storageTotalVal")) {
      $("storageTotalVal").textContent = (sum.total != null ? fmtQuantity(sum.total) : "--") + " " + unit;
    }
  }

  /* ---------- 存储详情表 ---------- */
  function renderTable(storage, redPct = 90, amberPct = 75) {
    const tbody = $("storageTableBody");
    if (!tbody || !storage || !storage.items) return;

    tbody.innerHTML = storage.items.map(it => {
      const ratio = it.total > 0 ? it.used / it.total : 0;
      const pctVal = ratio * 100;
      const pctStr = pctVal.toFixed(1);
      const { barClass, textClass } = storageUtilTone(pctVal, redPct, amberPct);

      return `
      <tr>
        <td class="text-mono text-muted">${escapeHTML(it.id)}</td>
        <td class="text-bold">${escapeHTML(it.name)}</td>
        <td><span class="badge-type ${escapeHTML(it.type)}">${escapeHTML((it.type || "").toUpperCase())}</span></td>
        <td>${fmtQuantity(it.total)} ${escapeHTML(it.unit || "GB")}</td>
        <td class="text-bold ${textClass}">${fmtQuantity(it.used)} ${escapeHTML(it.unit || "GB")}</td>
        <td>
          <div class="table-bar-box">
            <div class="table-bar ${barClass}">
              <i style="--bar-pct:${Math.min(100, pctVal)}%"></i>
            </div>
            <span class="text-mono ${textClass}">${pctStr}%</span>
          </div>
        </td>
        <td class="storage-sampled-at">${renderTimeHTML(it.sampledAt)}</td>
      </tr>`;
    }).join("");
  }

  function animateNum(el, to, options = {}) {
    if (!el) return;
    const { capDisplay } = options;
    const numTo = Number(to) || 0;
    const strTo = String(to ?? "0");
    const dotIdx = strTo.indexOf(".");
    const decimals = dotIdx >= 0 ? (strTo.length - dotIdx - 1) : 0;

    const from = parseFloat(el.dataset.v || "0") || 0;
    const dur = 500, t0 = performance.now();
    el.dataset.v = numTo;

    const formatVal = val => {
      const rounded = Math.round(val);
      if (capDisplay != null && decimals === 0 && rounded > capDisplay) {
        return "9999+";
      }
      return decimals > 0 ? val.toFixed(decimals) : rounded.toLocaleString("zh-CN");
    };

    const step = now => {
      const k = Math.min(1, (now - t0) / dur);
      const eased = 1 - Math.pow(1 - k, 3);
      const val = from + (numTo - from) * eased;
      el.textContent = formatVal(val);
      if (k < 1) requestAnimationFrame(step);
      else if (capDisplay != null) applyJobCountTitle(el, numTo);
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
    upd.textContent = "更新于 " + fmtTime(d.sampledAt);
    if (isStaleTime(d.sampledAt)) {
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
      localStorage.setItem("dc_overview_state", JSON.stringify(d));
    } catch (e) { }

    updateDataTimestamps(d);

    // 有 GPU 判定与 Desktop 一致：gpu.nodes.configured > 0（不单独展示 GPU 节点卡）
    const hasGpu = Boolean(d.gpu?.nodes?.configured > 0);
    const topCardsSection = $("topCardsSection");
    if (!topCardsSection) return;

    if (hasGpu) {
      topCardsSection.classList.remove("no-gpu");
      const gpuCards = d.gpu.cards || { online: 0, configured: 0, utilization: 0 };
      const gpuVram = d.gpu.vram || { online: 0, configured: 0, used: 0, unit: "GB" };
      const vramUnit = gpuVram.unit || "GB";

      const gpuUtil = gpuCards.utilization || 0;
      const gpuPct = Math.min(100, Math.max(0, gpuUtil * 100));
      const gpuValEl = $("kpiGpuUtilVal");
      if (gpuValEl) gpuValEl.textContent = gpuPct.toFixed(1) + "%";
      const gpuBarEl = $("barGpuUtil");
      if (gpuBarEl) gpuBarEl.style.setProperty("--bar-pct", gpuPct.toFixed(1) + "%");

      animateNum($("kpiGpuMemTotal"), fmtQuantity(gpuVram.online));
      animateNum($("kpiGpuMemUsed"), fmtQuantity(gpuVram.used));
      if ($("kpiGpuMemTotalUnit")) $("kpiGpuMemTotalUnit").textContent = vramUnit;
      if ($("kpiGpuMemUsedUnit")) $("kpiGpuMemUsedUnit").textContent = vramUnit;
      if (gpuVram.online > 0) setRing("ringGpuMem", "ringGpuMemText", gpuVram.used / gpuVram.online);
      else setRing("ringGpuMem", "ringGpuMemText", 0);
    } else {
      topCardsSection.classList.add("no-gpu");
    }

    const cpuConfigured = d.cpu ? d.cpu.configured : 1;
    const onlineCores = d.cpu?.online || 1;
    const alertTh = resolveAlertThresholds(d.alert);

    if (d.nodes) {
      const nodesConfigured = d.nodes.configured || 0;
      const nodesOnline = d.nodes.online || 0;
      animateNum($("kpiNodesConfigured"), nodesConfigured);
      animateNum($("kpiNodesOnline"), nodesOnline);
      animateNum($("kpiNodesMaintenance"), d.nodes.maintenance || 0);
      animateNum($("kpiNodesOffline"), d.nodes.offline || 0);
      setRing("ringNodes", "ringNodesText", nodesConfigured > 0 ? nodesOnline / nodesConfigured : 0);
    }

    if (d.cpu) {
      const cpuUtil = d.cpu.utilization || 0;
      const cpuPct = Math.min(100, Math.max(0, cpuUtil * 100));
      animateNum($("kpiCpuOnline"), d.cpu.online);
      if ($("kpiCpuTotal")) $("kpiCpuTotal").textContent = "/" + fmtCount(cpuConfigured);
      const cpuValEl = $("kpiCpuUtilVal");
      if (cpuValEl) cpuValEl.textContent = cpuPct.toFixed(1) + "%";
      const cpuBarEl = $("barCpuUtil");
      if (cpuBarEl) cpuBarEl.style.setProperty("--bar-pct", cpuPct.toFixed(1) + "%");
    }

    if (d.load) {
      renderLoadNumbers(d.load["1m"], d.load["5m"], d.load["15m"], onlineCores, alertTh.loadBusy, alertTh.loadOverload);
    }

    if (d.memory) {
      const memConfigured = d.memory.configured;
      const memOnline = d.memory.online;
      const memUnit = d.memory.unit || "GB";
      animateNum($("kpiMemOnlineStatic"), fmtQuantity(memOnline));
      if ($("kpiMemTotalStatic")) $("kpiMemTotalStatic").textContent = "/" + fmtQuantity(memConfigured);

      // 内存卡分母取在线容量（与 Desktop / engine memory.online 口径一致）
      animateNum($("kpiMemTotalVal"), fmtQuantity(memOnline));
      animateNum($("kpiMemUsedVal"), fmtQuantity(d.memory.used));
      if ($("kpiMemTotalUnit")) $("kpiMemTotalUnit").textContent = memUnit;
      if ($("kpiMemUsedUnit")) $("kpiMemUsedUnit").textContent = memUnit;
      if (memOnline > 0) setRing("ringMemUsed", "ringMemUsedText", d.memory.used / memOnline);
      else setRing("ringMemUsed", "ringMemUsedText", 0);
    }

    const sched = (d.scheduler && d.scheduler.hosts) || { online: 0, total: 0 };
    const schedRatio = sched.total > 0 ? sched.online / sched.total : 0;
    $("schedNodesTag").innerHTML = `<span class="text-emerald text-bold text-mono">${fmtCount(sched.online)}</span><span class="text-muted text-mono"> / ${fmtCount(sched.total)}</span>`;
    if ($("schedHostBadge")) $("schedHostBadge").title = (schedRatio * 100).toFixed(1) + "%";
    setRing("ringSchedNodes", null, schedRatio);

    renderStorageDonut(d.storage, alertTh.storageRed, alertTh.storageAmber);
    renderQueueTable(d.scheduler?.queues);
    renderTable(d.storage, alertTh.storageRed, alertTh.storageAmber);

    if (d.scheduler?.jobs) {
      const jobs = d.scheduler.jobs;
      const totalJobs = (jobs.running || 0) + (jobs.pending || 0) + (jobs.suspended || 0) + (jobs.completed || 0) + (jobs.failed || 0);
      animateNum($("jobRunning"), jobs.running, { capDisplay: JOB_COUNT_DISPLAY_MAX });
      animateNum($("jobPending"), jobs.pending, { capDisplay: JOB_COUNT_DISPLAY_MAX });
      animateNum($("jobSuspended"), jobs.suspended, { capDisplay: JOB_COUNT_DISPLAY_MAX });
      animateNum($("jobDone"), jobs.completed, { capDisplay: JOB_COUNT_DISPLAY_MAX });
      animateNum($("jobFailed"), jobs.failed, { capDisplay: JOB_COUNT_DISPLAY_MAX });

      const jobTotalEl = $("jobTotalText");
      if (jobTotalEl) {
        jobTotalEl.textContent = fmtJobCountDisplay(totalJobs);
        applyJobCountTitle(jobTotalEl, totalJobs);
      }
      setRing("ringJobTotal", null, totalJobs > 0 ? (jobs.running || 0) / totalJobs : 0);
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

    const sseUrl = `/api/overview/cluster/stream?token=${encodeURIComponent(PUBLIC_OVERVIEW_TOKEN)}`;
    try {
      sseSource = new EventSource(sseUrl);
      sseSource.addEventListener("overview", function (event) {
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
  const fullscreenBtn = $("fullscreenBtn");
  if (fullscreenBtn) fullscreenBtn.addEventListener("click", toggleFullscreen);
  render(state);
  initSSE();
  setInterval(tickClock, 1000);
  tickClock();

})();
