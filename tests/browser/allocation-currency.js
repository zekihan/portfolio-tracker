// Run on a dashboard with nonzero holdings, once with ?currency=TRY and once with ?currency=USD.
(async () => {
  const currency = document.documentElement.dataset.currency;
  const response = await fetch(`/api/dashboard/allocation?type=type&currency=${encodeURIComponent(currency)}`);
  if (!response.ok) throw new Error(`Allocation API returned ${response.status}`);
  const expected = await response.json();
  const expectedTotal = Object.values(expected.by_type).reduce((sum, value) => sum + value, 0);
  if (!(expectedTotal > 0)) throw new Error('Regression requires a nonzero portfolio fixture');
  const previous = PortfolioApp.state().charts.allocation;
  PortfolioApp.loadAllocationChart('type');
  const deadline = performance.now() + 5000;
  while (performance.now() < deadline) {
    const chart = PortfolioApp.state().charts.allocation;
    if (chart && chart !== previous) {
      const actualTotal = chart.data.datasets[0].data.reduce((sum, value) => sum + value, 0);
      if (Math.abs(actualTotal - expectedTotal) > Math.abs(expectedTotal) * 1e-9) {
        throw new Error(`Chart total ${actualTotal} differs from ${currency} API total ${expectedTotal}`);
      }
      return { currency, chartTotal: actualTotal, apiTotal: expectedTotal };
    }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error('Allocation chart did not refresh');
})()
