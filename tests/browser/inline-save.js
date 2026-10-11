// Run ONLY against a disposable populated holdings fixture. Saves and restores real data.
(async () => {
  const row = document.querySelector('tr[data-id]');
  if (!row) throw new Error('Regression requires a holding');
  const url = `/api/holdings/${row.dataset.id}`;
  async function read() {
    const response = await fetch(url);
    if (!response.ok) throw new Error('Cannot read fixture holding');
    return response.json();
  }
  const original = await read();
  const expected = {...original};
  const results = [];
  let changed = false;
  try {
    for (const [field, value] of [ ['notes','Saved notes, $ intact'], ['basket','Long term basket'], ['location','Safe location'], ['name','Renamed holding'], ['ticker','NEW'], ['quantity',12.5], ['est_price',90], ['notes',''], ['basket',''], ['location',''], ['quantity',0], ['est_price',0] ]) {
      const cell = row.querySelector(`td.editable-cell[data-field="${field}"]`);
      cell.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}));
      const input = cell.querySelector('input');
      if (!input) throw new Error(`${field} editor did not open`);
      input.value = String(value);
      input.dispatchEvent(new Event('blur'));
      const deadline = performance.now()+5000;
      while (cell.querySelector('input') && performance.now()<deadline) await new Promise(resolve=>setTimeout(resolve,50));
      const stored = await read();
      changed = changed || JSON.stringify(stored)!==JSON.stringify(original);
      expected[field] = value;
      for (const key of Object.keys(expected)) {
        if (key !== 'updated_at' && JSON.stringify(stored[key]) !== JSON.stringify(expected[key])) throw new Error(`${field} update mismatched persisted ${key}`);
      }
      if (cell.querySelector('input')) throw new Error(`${field} did not finish saving`);
      results.push({field, saved:true});
    }
    return results;
  } finally {
    if (changed) {
      const payload = Object.fromEntries(['name','ticker','notes','basket','location','quantity','est_price'].map(field=>[field,original[field]]));
      const restored = await fetch(url,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
      if (!restored.ok) throw new Error('Could not restore disposable fixture');
      const stored=await read();
      for (const key of Object.keys(original)) {
        if (key!=='updated_at' && JSON.stringify(stored[key])!==JSON.stringify(original[key])) throw new Error(`Fixture restoration mismatch: ${key}`);
      }
    }
  }
})()
