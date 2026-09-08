// SPDX-License-Identifier: Apache-2.0
// Shared behavior for all trusted templates. Credentials remain in HttpOnly cookies.
(() => {
  'use strict';
  const copy = {
    ko: { language:'언어', label:'입장 상태', enter:'입장하기', retry:'다시 확인하기', rejoin:'다시 대기하기', reassurance:'새로고침해도 순서는 유지돼요.',
      queued:['순서를 기다리고 있어요','접속이 많아 잠시 대기 중이에요.','창을 닫지 않으면 입장 기회를 확인할 수 있어요.','대기 중','예상 대기 시간은 아직 계산 중이에요.'],
      ready:['입장할 준비가 됐어요','이제 서비스에 접속할 수 있어요.','아래 버튼을 눌러 입장해 주세요.','입장 가능','입장 기회는 제한된 시간 동안 유지돼요.'],
      admitted:['다시 입장할 수 있어요','입장 권한이 아직 유효해요.','아래 버튼을 눌러 원래 페이지로 이동해 주세요.','입장 가능','입장 버튼을 눌러도 대기열에 다시 등록되지 않아요.'],
      unavailable:['연결을 확인하고 있어요','잠시 입장 상태를 확인할 수 없어요.','기존 대기표로 다시 확인할게요.','확인 중','새 대기표를 자동으로 만들지 않아요.'],
      expired:['대기표가 만료됐어요','대기 시간이 지나 입장 기회가 종료됐어요.','다시 대기하려면 아래 버튼을 눌러 주세요.','만료됨','다시 대기하면 새로운 순서가 부여돼요.'] },
    en: { language:'Language', label:'Admission status', enter:'Enter now', retry:'Check again', rejoin:'Join again', reassurance:'Refreshing keeps your place in line.',
      queued:['You’re in line','We’re experiencing a little extra traffic.','Keep this window open to check when you can enter.','Waiting','We’re still calculating your estimated wait.'],
      ready:['You’re ready to enter','You can now access the service.','Use the button below to enter.','Ready','Your entry opportunity is available for a limited time.'],
      admitted:['You can enter again','Your admission is still valid.','Use the button to return to your original page.','Ready','Entering again will not create a new queue ticket.'],
      unavailable:['Checking your connection','We can’t check your admission status right now.','We’ll try again with your existing ticket.','Reconnecting','We won’t create a new ticket automatically.'],
      expired:['Your ticket has expired','Your entry opportunity has ended.','Use the button below to join again.','Expired','Joining again gives you a new place in line.'] }
  };
  const body = document.body, language = document.querySelector('#language');
  if(['ko','en'].includes(body.dataset.themeLocale))language.value=body.dataset.themeLocale;
  const claim = document.querySelector('#claim'), retry = document.querySelector('#retry');
  const rejoin = document.querySelector('#rejoin');
  let state = 'queued', timer, failures = 0, heartbeatTimer, heartbeatMs = 300000;
  function render(next = state) {
    state = next; body.dataset.state = state;
    const text = copy[language.value], values = text[state];
    const fields = { ...text, heading:values[0], description:values[1], instruction:values[2] };
    if(state==='queued'&&body.dataset.themeEnabled==='true'&&language.value===body.dataset.themeLocale){
      if(body.dataset.themeTitle)fields.heading=body.dataset.themeTitle;
      if(body.dataset.themeMessage)fields.description=body.dataset.themeMessage;
    }
    document.documentElement.lang = language.value;
    for (const node of document.querySelectorAll('[data-copy]')) {
      const value = fields[node.dataset.copy];
      if (node.textContent !== value) node.textContent = value;
    }
    const status = document.querySelector('#status');
    if (status.textContent !== values[3]) status.textContent = values[3];
    document.querySelector('#estimate').textContent = values[4];
    document.querySelector('#estimate').hidden=state==='queued'&&body.dataset.themeEnabled==='true'&&body.dataset.showEstimate!=='true';
    document.querySelector('#panel').setAttribute('aria-label', text.label);
    claim.hidden = !['ready','admitted'].includes(state);
    retry.hidden = state !== 'unavailable'; rejoin.hidden = state !== 'expired';
    document.querySelector('.reassurance').hidden = state === 'expired';
  }
  function delay(ms) { clearTimeout(timer); timer = setTimeout(poll, ms); }
  async function poll() {
    clearTimeout(timer);
    try {
      const response = await fetch(body.dataset.statusUrl, {credentials:'same-origin', cache:'no-store', signal:AbortSignal.timeout(5000)});
      if (response.status === 410 || response.status === 401) { render('expired'); return; }
      if (response.status === 429) {
        const seconds = Number(response.headers.get('Retry-After'));
        delay((Number.isFinite(seconds) && seconds >= 1 && seconds <= 60 ? seconds * 1000 : 3000) + Math.floor(Math.random()*500));
        return;
      }
      if (!response.ok) throw new Error('unavailable');
      const data = await response.json();
      if (!['queued','ready','admitted'].includes(data.state)) throw new Error('state');
      if (data.state === 'queued' && Number.isSafeInteger(data.heartbeatAfterMs) && data.heartbeatAfterMs >= 1 && data.heartbeatAfterMs <= 300000 && data.heartbeatAfterMs !== heartbeatMs) {
        heartbeatMs = data.heartbeatAfterMs;
        clearTimeout(heartbeatTimer); heartbeatTimer = setTimeout(heartbeat, heartbeatMs);
      }
      failures = 0; render(data.state);
      delay(Math.max(3000, Math.min(20000, data.pollAfterMs || 3000)) + Math.floor(Math.random()*500));
    } catch {
      render('unavailable'); failures++;
      delay(Math.min(20000, 3000 * 2 ** Math.min(failures, 3)) + Math.floor(Math.random()*500));
    }
  }
  async function heartbeat() {
    if (state === 'queued' || state === 'unavailable') {
      try {
        await fetch(body.dataset.heartbeatUrl, {method:'POST', credentials:'same-origin', headers:{'X-Waiting-Room-CSRF':body.dataset.return}, signal:AbortSignal.timeout(5000)});
      } catch { /* Poll owns the visible connection state; never rejoin here. */ }
    }
    heartbeatTimer = setTimeout(heartbeat, heartbeatMs);
  }
  language.addEventListener('change', () => render());
  retry.addEventListener('click', poll);
  claim.addEventListener('submit', () => { claim.querySelector('button').disabled = true; });
  addEventListener('pageshow', event => {
    claim.querySelector('button').disabled = false;
    if (event.persisted) { poll(); clearTimeout(heartbeatTimer); heartbeat(); }
  });
  addEventListener('pagehide', () => { clearTimeout(timer); clearTimeout(heartbeatTimer); });
  render(); poll(); heartbeatTimer = setTimeout(heartbeat, heartbeatMs);
})();
