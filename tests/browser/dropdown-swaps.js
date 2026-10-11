// Run on a freshly loaded holdings page containing at least one holding.
(() => {
  const toggle = document.querySelector('.dropdown-toggle');
  if (!toggle) throw new Error('Regression requires a holding action dropdown');
  const dropdown = toggle.closest('.dropdown');
  const menu = dropdown.querySelector('.dropdown-menu');
  function checkClick(button, expected) {
    button.click();
    const expanded = button.getAttribute('aria-expanded') === 'true';
    const hidden = button.closest('.dropdown').querySelector('.dropdown-menu').getAttribute('aria-hidden') === 'true';
    if (expanded !== expected || hidden === expected) {
      throw new Error(`Dropdown click expected expanded=${expected}, got expanded=${expanded}, hidden=${hidden}`);
    }
  }
  checkClick(toggle, true);
  checkClick(toggle, false);
  for (let swaps = 1; swaps <= 3; swaps++) {
    document.body.dispatchEvent(new CustomEvent('htmx:afterSwap', {bubbles: true, detail: {target: dropdown}}));
    checkClick(toggle, true);
    checkClick(toggle, false);
  }
  // Delegation must also support newly inserted dropdowns without reinitialization.
  const inserted = dropdown.cloneNode(true);
  dropdown.parentElement.appendChild(inserted);
  try {
    const button = inserted.querySelector('.dropdown-toggle');
    checkClick(button, true);
    document.body.click();
    if (button.getAttribute('aria-expanded') !== 'false' || inserted.querySelector('.dropdown-menu').getAttribute('aria-hidden') !== 'true') {
      throw new Error('Outside click did not close the inserted dropdown');
    }
  } finally {
    inserted.remove();
  }
  return {swapsChecked: 3, insertedDropdownWorks: true, originalMenuClosed: menu.getAttribute('aria-hidden') === 'true'};
})()
