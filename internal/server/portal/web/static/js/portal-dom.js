/* 门户 / 大屏共享 DOM 工具（须在页面脚本之前加载） */
(function (global) {
  "use strict";

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>"']/g, char => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    }[char]));
  }

  global.dcPortalEscapeHTML = escapeHTML;
})(typeof globalThis !== "undefined" ? globalThis : window);
