// Run on a dashboard fixture with nonzero holdings, basket, and location.
(async () => {
  const currency = document.documentElement.dataset.currency;
  const response = await fetch(`/api/dashboard/allocation?currency=${encodeURIComponent(currency)}`);
  if (!response.ok) throw new Error(`Allocation API returned ${response.status}`);
  const data = await response.json();
  const results = [];
  for (const [view, key] of Object.entries({type: 'by_type', basket: 'by_basket', location: 'by_loc', currency: 'by_curr'})) {
    const expected = Object.values(data[key]).reduce((sum, value) => sum + value, 0);
    if (!(expected > 0)) throw new Error(`Regression requires nonzero ${view} allocation`);
    const previous = PortfolioApp.state().charts.allocation;
    PortfolioApp.loadAllocationChart(view);
    const deadline = performance.now() + 5000;
    let actual;
    while (performance.now() < deadline) {
      const chart = PortfolioApp.state().charts.allocation;
      if (chart && chart !== previous) {
        actual = chart.data.datasets[0].data.reduce((sum, value) => sum + value, 0);
        break;
      }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    if (actual === undefined || Math.abs(actual - expected) > expected * 1e-9) {
      throw new Error(`${view} chart total ${actual} differs from API total ${expected}`);
    }
    results.push({view, total: actual});
  }
  return results;
})()
