(() => {
  try {
    const saved = localStorage.getItem('bindery.theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const dark = saved === 'dark' || (!saved && prefersDark);
    if (dark) document.documentElement.classList.add('dark');
  } catch (_e) {}
})();
