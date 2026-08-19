/* ============================================================
   设计云平台 (Design Cloud Platform) — 统一主题控制 (Theme Controller)
   防闪烁 (FOUC) 自动初始化与亮/暗主题无缝切换
   ============================================================ */
(function (global) {
  const THEME_KEY = 'dc-dashboard-theme';

  function getTheme() {
    return localStorage.getItem(THEME_KEY) === 'light' ? 'light' : 'dark';
  }

  function applyTheme(t) {
    const theme = t === 'light' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem(THEME_KEY, theme);
    const btn = document.getElementById('themeToggle');
    if (btn) btn.textContent = theme === 'dark' ? '🌙' : '☀️';
  }

  // 立即执行防闪烁
  applyTheme(getTheme());

  global.addEventListener('DOMContentLoaded', function () {
    const btn = document.getElementById('themeToggle');
    if (btn) {
      btn.textContent = getTheme() === 'dark' ? '🌙' : '☀️';
      btn.addEventListener('click', function () {
        applyTheme(getTheme() === 'dark' ? 'light' : 'dark');
      });
    }
  });
})(window);
