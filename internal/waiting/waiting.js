// SPDX-License-Identifier: Apache-2.0
// Shared behavior for all trusted templates. Credentials remain in HttpOnly cookies.
(() => {
  'use strict';
  const copy = {
    ko: { language:'언어', label:'입장 상태', enter:'입장하기', retry:'다시 확인하기', rejoin:'다시 대기하기', reassurance:'새로고침해도 순서는 유지돼요.',
      queued:['순서를 기다리고 있어요','접속이 많아 잠시 대기 중이에요.','순서가 되면 자동으로 입장해요.','대기 중','예상 대기 시간은 아직 계산 중이에요.'],
      ready:['자동으로 입장하고 있어요','이제 서비스에 접속할 수 있어요.','잠시 후 원래 페이지로 이동해요.','입장 중','버튼을 누르거나 새로고침할 필요 없어요.'],
      admitted:['원래 페이지로 이동하고 있어요','입장 권한이 아직 유효해요.','잠시만 기다려 주세요.','입장 중','대기열에 다시 등록하지 않고 자동으로 이동해요.'],
      unavailable:['연결을 확인하고 있어요','잠시 입장 상태를 확인할 수 없어요.','기존 대기표로 다시 확인할게요.','확인 중','새 대기표를 자동으로 만들지 않아요.'],
      expired:['대기표가 만료됐어요','대기 시간이 지나 입장 기회가 종료됐어요.','다시 대기하려면 아래 버튼을 눌러 주세요.','만료됨','다시 대기하면 새로운 순서가 부여돼요.'] },
    en: { language:'Language', label:'Admission status', enter:'Enter now', retry:'Check again', rejoin:'Join again', reassurance:'Refreshing keeps your place in line.',
      queued:['You’re in line','We’re experiencing a little extra traffic.','Keep this window open. You’ll enter automatically when it’s your turn.','Waiting','We’re still calculating your estimated wait.'],
      ready:['Entering automatically','You can now access the service.','We’ll take you to your original page shortly.','Entering','There’s no need to click or refresh.'],
      admitted:['Returning to your page','Your admission is still valid.','Please wait a moment.','Entering','You’ll continue automatically without joining the queue again.'],
      unavailable:['Checking your connection','We can’t check your admission status right now.','We’ll try again with your existing ticket.','Reconnecting','We won’t create a new ticket automatically.'],
      expired:['Your ticket has expired','Your entry opportunity has ended.','Use the button below to join again.','Expired','Joining again gives you a new place in line.'] }
  };
  const body = document.body, language = document.querySelector('#language');
  if(['ko','en'].includes(body.dataset.themeLocale))language.value=body.dataset.themeLocale;
  const claim = document.querySelector('#claim'), retry = document.querySelector('#retry');
  const rejoin = document.querySelector('#rejoin');
  let progress = null;
  let state = 'queued', timer, failures = 0, heartbeatTimer, heartbeatMs = 300000, claimTimer, claiming = false, observation = 0;
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
    const en=language.value==='en';
    const queued=state==='queued';
    const position=document.querySelector('#queue-position');
    position.hidden=!queued;
    document.querySelector('#position-label').textContent=en?'Your approximate position':'내 대기 순번';
    const ahead=queued&&Number.isSafeInteger(progress?.usersAhead)&&progress.usersAhead>=0?progress.usersAhead:null;
    document.querySelector('#position-value').textContent=ahead===null?(en?'Checking…':'확인 중…'):(en?`About #${(ahead+1).toLocaleString('en-US')}`:`약 ${(ahead+1).toLocaleString('ko-KR')}번째`);
    document.querySelector('#position-detail').textContent=ahead===null?(en?'We’ll update your place shortly.':'대기 순번을 확인하고 있어요.'):(en?`${ahead.toLocaleString('en-US')} visitors ahead of you`:`내 앞에 약 ${ahead.toLocaleString('ko-KR')}명이 있어요.`);
    let estimate=values[4];
    const range=progress?.estimatedWaitSeconds;
    if(queued&&progress?.admissionPaused===true)estimate=en?'Admissions are paused. Waiting to resume.':'입장 재개 대기 중이에요.';
    else if(queued&&range&&Number.isSafeInteger(range.min)&&Number.isSafeInteger(range.max)&&range.min>=0&&range.max>=range.min){
      const low=Math.max(1,Math.ceil(range.min/60)),high=Math.max(low,Math.ceil(range.max/60));
      estimate=range.max<60?(en?'Estimated wait: under a minute':'예상 대기시간: 약 1분 이내'):(en?`Estimated wait: about ${low===high?low:`${low}–${high}`} min`:`예상 대기시간: 약 ${low===high?low:`${low}~${high}`}분`);
    }
    document.querySelector('#estimate').textContent=estimate;
    const note=document.querySelector('#estimate-note');
    note.hidden=!queued;
    note.textContent=en?'Position and time are estimates and update as admissions change.':'순번과 시간은 대략적인 안내이며 입장 상황에 따라 달라져요.';
    document.querySelector('#estimate').hidden=state==='queued'&&body.dataset.themeEnabled==='true'&&body.dataset.showEstimate!=='true';
    document.querySelector('#panel').setAttribute('aria-label', text.label);
    retry.hidden = state !== 'unavailable'; rejoin.hidden = state !== 'expired';
    document.querySelector('.reassurance').hidden = state === 'expired';
  }
  function delay(ms) { clearTimeout(timer); timer = setTimeout(poll, ms); }
  async function poll() {
    clearTimeout(timer);
    if (claiming) return;
    const current = ++observation;
    clearTimeout(claimTimer);
    try {
      const response = await fetch(body.dataset.statusUrl, {credentials:'same-origin', cache:'no-store', signal:AbortSignal.timeout(5000)});
      if (current !== observation || claiming) return;
      if (response.status === 410 || response.status === 401) { progress=null; render('expired'); return; }
      if (response.status === 429) {
        const seconds = Number(response.headers.get('Retry-After'));
        delay((Number.isFinite(seconds) && seconds >= 1 && seconds <= 60 ? seconds * 1000 : 3000) + Math.floor(Math.random()*500));
        return;
      }
      if (!response.ok) throw new Error('unavailable');
      const data = await response.json();
      if (current !== observation || claiming) return;
      if (!['queued','ready','admitted'].includes(data.state)) throw new Error('state');
      if (data.state === 'queued' && Number.isSafeInteger(data.heartbeatAfterMs) && data.heartbeatAfterMs >= 1 && data.heartbeatAfterMs <= 300000 && data.heartbeatAfterMs !== heartbeatMs) {
        heartbeatMs = data.heartbeatAfterMs;
        clearTimeout(heartbeatTimer); heartbeatTimer = setTimeout(heartbeat, heartbeatMs);
      }
      progress=data; failures = 0; render(data.state);
      if (['ready','admitted'].includes(state)) {
        clearTimeout(heartbeatTimer);
        // Keep the signed, server-verified native POST/303 flow. A short delay
        // also bounds retries if a rejected claim redirects back to this page.
        clearTimeout(claimTimer);
        claimTimer = setTimeout(() => claim.requestSubmit(), 3000);
        return;
      }
      delay(Math.max(3000, Math.min(20000, data.pollAfterMs || 3000)) + Math.floor(Math.random()*500));
    } catch {
      if (current !== observation || claiming) return;
      progress=null; render('unavailable'); failures++;
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
  claim.addEventListener('submit', event => {
    if (claiming || !['ready','admitted'].includes(state)) { event.preventDefault(); return; }
    claiming = true;
    clearTimeout(timer); clearTimeout(heartbeatTimer); clearTimeout(claimTimer);
    claim.querySelector('button').disabled = true;
  });
  addEventListener('pageshow', event => {
    claim.querySelector('button').disabled = false;
    if (event.persisted) {
      claiming = false; progress=null; clearTimeout(claimTimer); render('queued');
      poll(); clearTimeout(heartbeatTimer); heartbeat();
    }
  });
  addEventListener('pagehide', () => { observation++; clearTimeout(timer); clearTimeout(heartbeatTimer); clearTimeout(claimTimer); });
  render(); poll(); heartbeatTimer = setTimeout(heartbeat, heartbeatMs);
})();
