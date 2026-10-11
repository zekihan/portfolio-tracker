// Run in the browser console after loading a portfolio page.
// Throws when bootstrap registers duplicate theme handlers or loses the page currency.
(() => {
  const root = document.documentElement;
  const toggle = document.getElementById('themeToggle');
  if (!toggle) throw new Error('Missing theme toggle');
  const before = root.getAttribute('data-theme');
  toggle.click();
  const after = root.getAttribute('data-theme');
  if (after === before) throw new Error('One theme click did not change the theme');
  toggle.click();
  if (root.getAttribute('data-theme') !== before) {
    throw new Error('Second theme click did not restore the original theme');
  }
  const currency = PortfolioApp.state().displayCurrency;
  if (currency !== root.dataset.currency) {
    throw new Error(`App currency ${currency} differs from page ${root.dataset.currency}`);
  }
  return { themeTogglesOnce: true, themeRestored: true, currency };
})()
