// Run on a disposable fixture. Run once to click the menu, then again after navigation to verify and restore settings.
(async () => {
  const key = 'qa-currency-switch';
  const pending = sessionStorage.getItem(key);
  const response = await fetch('/api/settings');
  if (!response.ok) throw new Error('Cannot read fixture settings');
  const settings = await response.json();
  if (pending) {
    const test = JSON.parse(pending);
    try {
      if (document.documentElement.dataset.currency !== test.target || settings.display_currency !== test.target || new URL(location.href).searchParams.get('currency') !== test.target) {
        throw new Error('Currency menu did not persist and render the selected currency');
      }
      if (location.pathname !== test.path) throw new Error('Currency switch changed the page');
      return {currency:test.target,persisted:true,pageRendered:true};
    } finally {
      const restored = await fetch('/api/settings/currency',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({currency:test.original})});
      if (!restored.ok) throw new Error('Could not restore fixture currency');
      const after = await (await fetch('/api/settings')).json();
      if (after.display_currency !== test.original) throw new Error('Fixture currency restoration failed');
      sessionStorage.removeItem(key);
    }
  }
  const deadline = performance.now()+5000;
  while (!document.querySelector('#currency-menu button') && performance.now()<deadline) await new Promise(resolve=>setTimeout(resolve,50));
  const currencies = ['TRY', 'USD', 'EUR'];
  const target = currencies[(currencies.indexOf(document.documentElement.dataset.currency) + 1) % currencies.length];
  document.querySelector('.currency-btn').click();
  const option = [...document.querySelectorAll('#currency-menu button')].find(button=>button.textContent.includes(`(${target})`));
  if (!option) throw new Error('Missing currency option');
  sessionStorage.setItem(key,JSON.stringify({target,original:settings.display_currency,path:location.pathname}));
  option.click();
  return {clicked:target,nextStep:'After navigation, run this script again to verify and restore the fixture setting'};
})()
