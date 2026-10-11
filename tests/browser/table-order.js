// Run on a populated holdings page. Uses a temporary cloned table, without persisted writes.
(() => {
  const source = document.querySelector('#holdings-table');
  const template = source?.querySelector('tbody tr[data-id]');
  if (!template) throw new Error('Regression requires a holding');
  const table = source.cloneNode(true);
  table.removeAttribute('id');
  const body = table.querySelector('tbody');
  body.removeAttribute('id');
  body.replaceChildren();
  const rows = [
    ['Zulu','Z',10,0,2], ['Alpha','A',2,100,1000], ['Mike','M',1000,2,10]
  ].map((values,index) => {
    const row=template.cloneNode(true); row.dataset.id=String(index);
    values.forEach((value,column)=>{
      row.cells[column].textContent=column>=2 ? Number(value).toLocaleString('de-DE') : String(value);
      if(column===3) row.cells[column].textContent+=' Stale';
      if(column>=2) row.cells[column].dataset.sortValue=String(value);
    });
    body.appendChild(row); return row;
  });
  document.body.appendChild(table);
  function expectOrder(expected) {
    const actual=[...body.rows].map(row=>Number(row.dataset.id));
    if(JSON.stringify(actual)!==JSON.stringify(expected)) throw new Error(`Row order ${actual} differs from ${expected}`);
  }
  try {
    for(const [key,ascending] of [['name',[1,2,0]],['ticker',[1,2,0]],['quantity',[1,0,2]],['current_price',[0,2,1]],['est_price',[0,2,1]]]) {
      const header=table.querySelector(`th[data-sort="${key}"]`);
      header.click(); expectOrder(ascending);
      header.click(); expectOrder([...ascending].reverse());
      header.click(); expectOrder([0,1,2]);
      if(body.rows.length!==rows.length) throw new Error('Sorting lost rows');
    }
    return {textAndNumericOrdering:true,originalOrderRestored:true,rowCount:body.rows.length};
  } finally { table.remove(); }
})()
