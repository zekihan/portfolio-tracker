// Run on a populated dashboard after initial chart loading.
(async () => {
  async function waitForCharts(previous) {
    const deadline = performance.now() + 5000;
    while (performance.now() < deadline) {
      const charts = PortfolioApp.state().charts;
      if (charts.portfolio && charts.allocation && charts.portfolio !== previous.portfolio && charts.allocation !== previous.allocation) return charts;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Both charts must redraw');
  }
  await waitForCharts({portfolio: null, allocation: null});
  const defaults = {...PortfolioApp.state().charts};
  document.body.dispatchEvent(new CustomEvent('portfolio-updated', {bubbles: true}));
  await waitForCharts(defaults);
  if (document.querySelector('.chart-tab[aria-selected="true"]').dataset.range !== '1y' || document.querySelector('.alloc-tab[aria-selected="true"]').dataset.alloc !== 'type') {
    throw new Error('Refresh changed default views');
  }
  const initial = {...PortfolioApp.state().charts};
  document.querySelector('.chart-tab[data-range="1m"]').click();
  document.querySelector('.alloc-tab[data-alloc="basket"]').click();
  const selected = await waitForCharts(initial);
  if (document.querySelector('.chart-tab[aria-selected="true"]').dataset.range !== '1m' || document.querySelector('.alloc-tab[aria-selected="true"]').dataset.alloc !== 'basket') {
    throw new Error('Could not select non-default views');
  }
  const previous = {...selected};
  document.body.dispatchEvent(new CustomEvent('portfolio-updated', {bubbles: true}));
  await waitForCharts(previous);
  const range = document.querySelector('.chart-tab[aria-selected="true"]').dataset.range;
  const allocation = document.querySelector('.alloc-tab[aria-selected="true"]').dataset.alloc;
  if (range !== '1m' || allocation !== 'basket') {
    throw new Error(`Refresh reset selected views to ${range}/${allocation}`);
  }
  return {range, allocation, chartsRedrawn: true};
})()
