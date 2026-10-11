// Run on a populated dashboard in TRY and USD; clicks each actual allocation tab.
(async () => {
  const currency = document.documentElement.dataset.currency;
  const response = await fetch(`/api/dashboard/allocation?currency=${encodeURIComponent(currency)}`);
  if (!response.ok) throw new Error(`Allocation API returned ${response.status}`);
  const allocations = await response.json();
  const results = [];
  for (const [view, key] of Object.entries({type: 'by_type', basket: 'by_basket', location: 'by_loc', currency: 'by_curr'})) {
    const expected = Object.values(allocations[key]).reduce((sum, value) => sum + value, 0);
    if (!(expected > 0)) throw new Error(`Regression requires nonzero ${view} allocation`);
    const button = document.querySelector(`.alloc-tab[data-alloc="${view}"]`);
    if (!button) throw new Error(`Missing ${view} tab`);
    const previous = PortfolioApp.state().charts.allocation;
    button.click();
    const deadline = performance.now() + 5000;
    let ready = false;
    while (performance.now() < deadline) {
      const canvas = document.getElementById('allocation-chart');
      if (canvas.textContent.trim()) throw new Error(`${view} tab inserted response text into canvas`);
      const chart = PortfolioApp.state().charts.allocation;
      if (chart && chart !== previous && button.getAttribute('aria-selected') === 'true') {
        const total = chart.data.datasets[0].data.reduce((sum, value) => sum + value, 0);
        if (Math.abs(total - expected) > expected * 1e-9) throw new Error(`${view} total mismatch`);
        const selected = [...document.querySelectorAll('.alloc-tab')].filter(tab => tab.classList.contains('active') && tab.getAttribute('aria-selected') === 'true');
        if (selected.length !== 1 || selected[0] !== button) throw new Error(`${view} selection mismatch`);
        results.push({view, total});
        ready = true;
        break;
      }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    if (!ready) throw new Error(`${view} tab did not redraw the chart`);
  }
  return {currency, results};
})()
