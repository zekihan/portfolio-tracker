// Run in the browser console on the dashboard after page load.
(async () => {
  if (!document.querySelector('.dashboard')) throw new Error('Not on the dashboard');
  if ([...document.scripts].some(script => script.textContent.includes('PortfolioApp.initCharts('))) {
    throw new Error('Dashboard invokes an unexported chart bootstrap');
  }
  const deadline = performance.now() + 5000;
  while (performance.now() < deadline) {
    const charts = PortfolioApp.state().charts;
    if (charts.portfolio && charts.allocation) {
      return { portfolioChartReady: true, allocationChartReady: true };
    }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error('Dashboard charts did not initialize through the normal app bootstrap');
})()
