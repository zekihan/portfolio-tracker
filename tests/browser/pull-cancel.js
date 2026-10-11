// Run on a fresh page scrolled to the top; exercises canceled touch gestures.
(() => {
  if (window.scrollY !== 0) throw new Error('Scroll to the top before running');
  let refreshes = 0;
  const originalFetch = window.fetch;
  window.fetch = (url, options) => {
    if (url === '/api/sources/refresh' && options?.method === 'POST') {
      refreshes++;
      // Reject the synthetic refresh to prevent navigation or provider requests.
      return Promise.resolve({ok:false});
    }
    return originalFetch(url, options);
  };
  function touch(type,y) {
    const touches = type === 'touchend' || type === 'touchcancel' ? [] : [new Touch({identifier:1,target:document.body,clientY:y})];
    document.body.dispatchEvent(new TouchEvent(type,{bubbles:true,cancelable:true,touches}));
  }
  try {
    touch('touchstart',100);
    touch('touchmove',180);
    if (document.body.style.transform !== 'translateY(40px)') throw new Error('Pull did not apply visual offset');
    touch('touchcancel',180);
    if (document.body.style.transform !== '' && document.body.style.transform !== 'translateY(0px)' && document.body.style.transform !== 'translateY(0)') {
      throw new Error('Canceled pull left the page translated');
    }
    touch('touchend',180);
    if (refreshes !== 0) throw new Error('Canceled gesture triggered refresh');
    touch('touchstart',100);
    touch('touchmove',180);
    touch('touchend',180);
    if (refreshes !== 1) throw new Error('Next deliberate pull did not refresh once');
    return {cancelRestoredPage:true,canceledRefreshes:0,nextPullWorks:true};
  } finally {
    window.fetch = originalFetch;
  }
})()
