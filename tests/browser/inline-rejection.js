// Run against a disposable populated holdings fixture; expects empty-name PATCH rejection.
(async () => {
  const cell = document.querySelector('td.editable-cell[data-field="name"]');
  if (!cell) throw new Error('Regression requires a holding');
  const id = cell.closest('tr').dataset.id;
  const original = cell.textContent.trim();
  const beforeResponse = await fetch(`/api/holdings/${id}`);
  if (!beforeResponse.ok) throw new Error('Could not read fixture holding');
  const before = await beforeResponse.json();
  const realFetch = window.fetch;
  const requests = [];
  window.fetch = (...args) => {
    const response = realFetch(...args);
    if (args[1]?.method === 'PATCH') requests.push(response.then(result => result.status));
    return response;
  };
  try {
    cell.dispatchEvent(new MouseEvent('dblclick', {bubbles: true}));
    const input = cell.querySelector('input');
    if (!input) throw new Error('Name editor did not open');
    input.value = '';
    input.dispatchEvent(new Event('blur'));
    const statuses = await Promise.all(requests);
    if (!statuses.length || statuses.some(status => status < 400)) throw new Error('Fixture did not reject the invalid PATCH');
    const deadline = performance.now() + 5000;
    while (cell.querySelector('input') && performance.now() < deadline) {
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    const afterResponse = await realFetch(`/api/holdings/${id}`);
    if (!afterResponse.ok) throw new Error('Could not re-read fixture holding');
    const after = await afterResponse.json();
    if (JSON.stringify(before) !== JSON.stringify(after)) throw new Error('Rejected update changed persisted holding');
    if (cell.querySelector('input') || cell.textContent !== original) throw new Error('Rejected update displayed an unsaved value');
    return {statuses, displayRestored: true, persistenceUnchanged: true};
  } finally {
    window.fetch = realFetch;
  }
})()
