/* 设计云平台 — 门户主页脚本（IIFE，不泄漏全局） */
(function () {
  const $ = id => document.getElementById(id);
  const escapeHTML = dcPortalEscapeHTML;

  const DEFAULT_SETUP_FILE = "DesignCloudDesktop-win-Setup.exe";
  const DEFAULT_BOOTSTRAP_FILE = "DesignCloudDesktop-Installer.exe";
  const PORTABLE_FILE = "DesignCloudDesktop-win-Portable.zip";

  const ICONS = {
    star: `<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 2l2.4 7.2H22l-6 4.4 2.3 7.2L12 16.4l-6.3 4.4 2.3-7.2-6-4.4h7.6L12 2z"/></svg>`,
    bolt: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M13 2L4 14h7l-1 8 9-12h-7l1-8z"/></svg>`,
    disk: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 4v5"/></svg>`,
    archive: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M21 8l-9-5-9 5 9 5 9-5z"/><path d="M3 8v8l9 5 9-5V8M12 13v8"/></svg>`,
    download: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M12 3v12m0 0l-4-4m4 4l4-4M4 21h16"/></svg>`,
    empty: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="4" y="5" width="16" height="14" rx="2"/><path d="M8 10h8M8 14h5"/><path d="M12 2v3"/></svg>`,
  };

  function formatSize(bytes) {
    if (!bytes || bytes <= 0) return "";
    const mb = bytes / (1024 * 1024);
    if (mb >= 1024) {
      return (mb / 1024).toFixed(2) + " GB";
    }
    return mb.toFixed(1) + " MB";
  }

  async function fetchData(url) {
    try {
      const res = await fetch(url);
      if (!res.ok) return null;
      return (await res.json()).data || null;
    } catch (e) {
      return null;
    }
  }

  function versionName(value) {
    if (!value) return "";
    const text = String(value);
    return text.startsWith("v") ? text : `v${text}`;
  }

  function downloadURL(file) {
    return `/api/desktop/releases/download/${encodeURIComponent(file)}`;
  }

  function setReleaseMeta(version, visible) {
    const meta = $("dlReleaseMeta");
    const chip = $("dlVersionChip");
    if (!meta || !chip) return;
    meta.hidden = !visible;
    if (version) {
      chip.textContent = "最新版本 " + version;
      chip.hidden = false;
    } else {
      chip.textContent = "";
      chip.hidden = true;
    }
  }

  function downloadCard(asset, opts) {
    const size = formatSize(asset.size);
    const meta = opts.portable ? "免安装" : size;
    const badge = opts.recommend
      ? `<span class="dl-recommend">${ICONS.star}推荐</span>`
      : "";
    return `<article class="dl-card">` +
      `<div class="dl-card-head">` +
        `<span class="dl-card-icon">${ICONS[opts.icon]}</span>` +
        `<div class="dl-card-title">` +
          `<div class="dl-title-line"><h3>${escapeHTML(opts.title)}</h3>${badge}</div>` +
          (meta ? `<span class="dl-card-meta">${escapeHTML(meta)}</span>` : "") +
        `</div>` +
      `</div>` +
      `<a class="dl-card-button${opts.recommend ? " primary" : ""}" href="${downloadURL(asset.file)}" download>${ICONS.download}${escapeHTML(opts.button)}</a>` +
    `</article>`;
  }

  function emptyState(title, desc) {
    return `<div class="dl-empty">` +
      `<span class="dl-empty-icon">${ICONS.empty}</span>` +
      `<h3>${escapeHTML(title)}</h3>` +
      `<p>${escapeHTML(desc)}</p>` +
    `</div>`;
  }

  async function renderDownloads(desktopEnabled) {
    const box = $("downloadContent");
    if (!box) return;

    if (desktopEnabled === false) {
      setReleaseMeta("", false);
      box.innerHTML = emptyState("客户端下载暂未开放", "管理员尚未发布 Windows 客户端，请稍后再试或联系管理员。");
      return;
    }

    // Setup / 在线安装器元数据必须走专用端点：releases.*.json 只登记 nupkg
    // 更新包；版本号仍取 OTA 清单，用于标识同批发布。
    const [installer, bootstrap, release] = await Promise.all([
      fetchData("/api/desktop/releases/installer"),
      fetchData("/api/desktop/releases/bootstrap-installer"),
      fetchData("/api/desktop/releases/latest?os=win"),
    ]);

    const version = versionName(release && release.version);
    const setup = installer ? {
      file: installer.fileName || DEFAULT_SETUP_FILE,
      size: installer.size,
    } : null;
    // 在线安装器依赖 Setup 元数据；Setup 缺失时一并隐藏，避免在线安装无法完成。
    const bootstrapAsset = (bootstrap && setup) ? {
      file: bootstrap.fileName || DEFAULT_BOOTSTRAP_FILE,
      size: bootstrap.size,
    } : null;
    // 便携版与 OTA 清单同批产出，清单缺失时不展示下载链接，避免死链。
    const portable = release ? { file: PORTABLE_FILE, size: 0 } : null;

    if (!setup && !portable) {
      setReleaseMeta("", false);
      box.innerHTML = emptyState("客户端暂未发布", "管理员尚未发布 Windows 客户端，请稍后再试或联系管理员。");
      return;
    }

    setReleaseMeta(version, true);

    const cards = [];
    if (setup) {
      cards.push(downloadCard(setup, {
        icon: "disk",
        title: "标准安装包",
        button: "下载标准安装包",
      }));
    }
    if (bootstrapAsset) {
      cards.push(downloadCard(bootstrapAsset, {
        recommend: true,
        icon: "bolt",
        title: "在线安装器",
        button: "下载在线安装器",
      }));
    }
    if (portable) {
      cards.push(downloadCard(portable, {
        icon: "archive",
        title: "便携版",
        button: "下载便携版",
        portable: true,
      }));
    }

    box.innerHTML = `<div class="dl-options" style="--dl-count:${cards.length}">${cards.join("")}</div>`;
  }

  async function loadVersion() {
    try {
      const d = (await (await fetch("/api/version")).json()).data;
      if (d && d.version) $("engineVersion").textContent = "v" + d.version;
      if (d && d.commit) $("engineCommit").textContent = (d.commit || "unknown").slice(0, 7);
      if (d && d.releaseDate) $("engineDate").textContent = d.releaseDate;
      renderDownloads(d ? d.desktopEnabled : true);
    } catch (e) {
      $("engineVersion").textContent = "unknown";
      $("engineCommit").textContent = "unknown";
      $("engineDate").textContent = "unknown";
      renderDownloads(true);
    }
  }

  loadVersion();
})();
