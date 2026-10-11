// Run on a dashboard fixture containing snapshots across all four time windows.
(async () => {
  const currency = document.documentElement.dataset.currency;
  const results = [];
  for (const [range, days] of Object.entries({'1m': 30, '3m': 90, '6m': 180, '1y': 365})) {
    const response = await fetch(`/api/history/chart?days=${days}`);
    if (!response.ok) throw new Error(`History API returned ${response.status}`);
    const expected = await response.json();
    if (!expected.length) throw new Error('Regression requires snapshot history');
    const button = document.querySelector(`.chart-tab[data-range="${range}"]`);
    if (!button) throw new Error(`Missing ${range} tab`);
    const previous = PortfolioApp.state().charts.portfolio;
    button.click();
    const deadline = performance.now() + 5000;
    let ready = false;
    while (performance.now() < deadline) {
      const canvas = document.getElementById('portfolio-chart');
      if (canvas.textContent.trim()) throw new Error(`${range} tab inserted response text into canvas`);
      const chart = PortfolioApp.state().charts.portfolio;
      if (chart && chart !== previous && button.getAttribute('aria-selected') === 'true') {
        const values = expected.map(row => currency === 'USD' ? row.value_usd : row.value_try);
        const labels = expected.map(row => new Date(row.timestamp).toLocaleDateString(undefined, {month: 'short', day: 'numeric'}));
        if (JSON.stringify(chart.data.datasets[0].data) !== JSON.stringify(values) || JSON.stringify(chart.data.labels) !== JSON.stringify(labels)) {
          throw new Error(`${range} chart differs from ${currency} history API`);
        }
        const selected = [...document.querySelectorAll('.chart-tab')].filter(tab => tab.classList.contains('active') && tab.getAttribute('aria-selected') === 'true');
        if (selected.length !== 1 || selected[0] !== button) throw new Error(`${range} selection mismatch`);
        results.push({range, points: values.length});
        ready = true;
        break;
      }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    if (!ready) throw new Error(`${range} tab did not redraw the chart`);
  }
  if (new Set(results.map(result => result.points)).size !== 4) {
    throw new Error('Fixture must have different point counts for all four windows');
  }
  return {currency, results};
})()
