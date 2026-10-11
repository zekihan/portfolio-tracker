// Run on a populated holdings page; edits DOM-only fixtures and cancels without saving.
(() => {
  const results = [];
  for (const [field, text] of Object.entries({basket: 'Long term, $ reserve', location: 'Safe ₺ deposit, room 2', notes: 'Keep $ and ₺, with spaces'})) {
    const cell = document.querySelector(`td.editable-cell[data-field="${field}"]`);
    if (!cell) throw new Error(`Missing editable ${field} cell`);
    const original = cell.textContent;
    cell.textContent = text;
    try {
      cell.dispatchEvent(new MouseEvent('dblclick', {bubbles: true}));
      const input = cell.querySelector('input');
      if (!input) throw new Error(`${field} editor did not open`);
      const actual = input.value;
      // Ensure any cleanup-triggered blur sees an unchanged value.
      input.value = text;
      input.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
      if (actual !== text) throw new Error(`${field} editor changed text: ${JSON.stringify(actual)}`);
      if (cell.textContent !== text || cell.querySelector('input')) throw new Error(`${field} cancel did not restore text`);
      results.push({field, textPreserved: true});
    } finally {
      cell.textContent = original;
    }
  }
  return results;
})()
