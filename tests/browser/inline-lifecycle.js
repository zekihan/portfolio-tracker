// Run ONLY on a disposable populated holdings page; performs and restores real writes.
(async () => {
  const cell = document.querySelector('td.editable-cell[data-field="notes"]');
  if (!cell) throw new Error('Regression requires a holding');
  const url = `/api/holdings/${cell.closest('tr').dataset.id}`;
  const realFetch = window.fetch;
  async function read() {
    const response = await realFetch(url);
    if (!response.ok) throw new Error('Could not read fixture holding');
    return response.json();
  }
  const original = await read();
  const requests = [];
  window.fetch = (...args) => {
    const response = realFetch(...args);
    if (args[0] === url && args[1]?.method === 'PATCH') requests.push(response.then(result => result.status));
    return response;
  };
  try {
    cell.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}));
    const input = cell.querySelector('input');
    input.value = 'Lifecycle saved notes';
    input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
    input.dispatchEvent(new Event('blur'));
    await Promise.all(requests);
    const deadline = performance.now()+5000;
    while (cell.querySelector('input') && performance.now()<deadline) await new Promise(resolve=>setTimeout(resolve,50));
    const saved = await read();
    if (saved.notes !== 'Lifecycle saved notes') throw new Error('Save did not persist');
    if (requests.length !== 1) throw new Error(`One edit sent ${requests.length} PATCH requests`);
    cell.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}));
    const canceled = cell.querySelector('input');
    canceled.value = 'Canceled notes must not persist';
    canceled.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
    canceled.dispatchEvent(new Event('blur'));
    await Promise.all(requests);
    const after = await read();
    if (requests.length !== 1 || after.notes !== saved.notes || cell.textContent !== saved.notes) throw new Error('Canceled edit was saved');
    return {saveRequests:requests.length,escapeCanceled:true,persistenceVerified:true};
  } finally {
    window.fetch = realFetch;
    const restored = await realFetch(url,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({notes:original.notes})});
    if (!restored.ok || (await read()).notes !== original.notes) throw new Error('Could not restore fixture notes');
  }
})()
