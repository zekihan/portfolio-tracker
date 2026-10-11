// Run on a fresh page scrolled to the top. Exercises the real touch event handlers.
(() => {
  if (window.scrollY !== 0) throw new Error('Scroll to the top before running');
  let refreshes = 0;
  const count = () => refreshes++;
  document.body.addEventListener('portfolio-updated', count);
  function touch(type, y) {
    const touches = type === 'touchend' ? [] : [new Touch({identifier:1,target:document.body,clientY:y})];
    document.body.dispatchEvent(new TouchEvent(type,{bubbles:true,cancelable:true,touches}));
  }
  try {
    touch('touchstart',100);
    touch('touchend',100);
    if (refreshes !== 0) throw new Error('An initial tap triggered refresh');
    touch('touchstart',100);
    touch('touchmove',180);
    touch('touchend',180);
    if (refreshes !== 1) throw new Error('A deliberate pull must refresh exactly once');
    for (let taps=0;taps<3;taps++) {
      touch('touchstart',100);
      touch('touchend',100);
      if (refreshes !== 1) throw new Error('Tap reused the previous pull position and refreshed');
    }
    touch('touchstart',100);
    touch('touchmove',130);
    touch('touchend',130);
    if (refreshes !== 1) throw new Error('A short pull triggered refresh');
    return {refreshes,subsequentTapsIgnored:true,shortPullIgnored:true};
  } finally {
    document.body.removeEventListener('portfolio-updated',count);
  }
})()
