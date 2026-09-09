// SPDX-License-Identifier: Apache-2.0
(() => {
  const body = document.body, endpoint = body.dataset.prepareUrl, target = body.dataset.target;
  if (!endpoint || !target) return;
  const notice = document.querySelector('#join-notice'), retry = document.querySelector('#join-retry'), rejoin = document.querySelector('#rejoin');
  let running = false;
  const message = value => { if (notice) { notice.hidden = false; notice.textContent = value; } };
  async function start(event) {
    event?.preventDefault();
    if (running) return;
    running = true;
    if (retry) retry.hidden = true;
    message('연결 확인 중');
    try {
      // A cross-tab lock covers the cookie handshake AND the first queue reply.
      // No queue/admission credential is exposed to JS or local storage.
      if (!navigator.locks?.request) throw new Error('unsupported');
      await navigator.locks.request('waiting-room/join/' + body.dataset.room, async () => {
        for (const confirm of [false, true]) {
          const response = await fetch(endpoint, {method:'POST', signal:AbortSignal.timeout(15000), credentials:'same-origin', cache:'no-store', redirect:'error', headers:{'Content-Type':'application/json'}, body:JSON.stringify({target,confirm,restart:body.dataset.bootstrap !== "true" || body.dataset.restart === "true"})});
          if (response.status === 428) throw new Error('cookies');
          if (response.status !== 204) throw new Error('connection');
        }
        const response = await fetch(target, {signal:AbortSignal.timeout(15000), credentials:'same-origin', cache:'no-store', headers:{Accept:'text/html'}});
        if (!response.ok || new URL(response.url).origin !== location.origin) throw new Error('connection');
        // Cookie processing finishes before fetch resolves. Other tabs can now
        // resume the same credential, with a separate sealed target per tab.
        location.replace(response.url);
      });
    } catch (error) {
      message(error.message === 'cookies' ? '쿠키를 허용한 뒤 다시 연결해 주세요.' : error.message === 'unsupported' ? '최신 Chrome, Firefox 또는 Safari에서 다시 연결해 주세요.' : '연결을 완료하지 못했어요. 같은 순서를 유지하며 다시 연결할 수 있어요.');
      if (retry) { retry.hidden = false; retry.focus(); }
    } finally { running = false; }
  }
  retry?.addEventListener('click', start);
  rejoin?.addEventListener('click', start);
  if (body.dataset.bootstrap === 'true') start();
})();
