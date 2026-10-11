// Run on the holdings page; checks direction controls before and after real HTMX swaps.
(async () => {
  const header = document.querySelector('th[data-sort="quantity"]');
  if (!header) throw new Error('Missing quantity sort header');
  const target = document.createElement('div');
  document.body.appendChild(target);
  function checkCycle(th) {
    const table = th.closest('table');
    for (const direction of ['asc', 'desc', 'none']) {
      th.click();
      if (direction === 'none') {
        if (table.dataset.sort || table.dataset.sortDir || th.classList.contains('sort-asc') || th.classList.contains('sort-desc')) {
          throw new Error('Third click did not clear sorting');
        }
      } else if (table.dataset.sort !== th.dataset.sort || table.dataset.sortDir !== direction || !th.classList.contains(`sort-${direction}`) || th.classList.contains(`sort-${direction === 'asc' ? 'desc' : 'asc'}`)) {
        throw new Error(`Sort click expected ${direction}, got ${table.dataset.sortDir}`);
      }
    }
  }
  try {
    checkCycle(header);
    for (let swaps = 1; swaps <= 3; swaps++) {
      await htmx.ajax('GET', '/api/settings', {target, swap: 'innerHTML'});
      checkCycle(header);
    }
    const inserted = header.closest('table').cloneNode(true);
    target.replaceChildren(inserted);
    checkCycle(inserted.querySelector('th[data-sort="quantity"]'));
    return {realSwapsChecked: 3, directionCycle: ['asc', 'desc', 'none'], insertedTableWorks: true};
  } finally {
    target.remove();
  }
})()
