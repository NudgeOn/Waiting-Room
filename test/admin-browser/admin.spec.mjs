// SPDX-License-Identifier: Apache-2.0
import {accessibility} from './accessibility.mjs';
import {test,expect} from './fixtures.mjs';
import {spawn} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {nativeDateInput} from '../localbeta/date-input.mjs';

async function capture(page,options,consoleErrors,browserName){
  expect(consoleErrors).toEqual([]);
  await page.evaluate(()=>window.scrollTo(0,0));
  await page.screenshot({...options,caret:'initial'});
  // Playwright WebKit injects an empty stylesheet during screenshot preparation.
  // Require a clean application before capture and only this exact tool warning.
  expect(consoleErrors).toEqual(browserName==='webkit'?["Refused to apply a stylesheet because its hash, its nonce, or 'unsafe-inline' does not appear in the style-src directive of the Content Security Policy."]:[]);
  consoleErrors.length=0;
}

test('native schedule date fixture covers every second without fill normalization failures',async({page})=>{
  await page.setContent('<input type="datetime-local" step="1" aria-label="fixture date">');
  const field=page.getByLabel('fixture date');
  for(let second=0;second<60;second++){
    const source='2026-09-06T17:18:'+String(second).padStart(2,'0'),value=nativeDateInput(source);
    await field.fill(value);await expect(field).toHaveValue(value);
    expect(await field.evaluate(el=>Number.isNaN(el.valueAsNumber))).toBe(false);
  }
});

function otp(key){
  let bits='';for(const char of key)bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char).toString(2).padStart(5,'0');
  const bytes=Buffer.from(bits.match(/.{8}/g).map(v=>parseInt(v,2)));const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
  const mac=crypto.createHmac('sha1',bytes).update(counter).digest();const offset=mac.at(-1)&15;return String((mac.readUInt32BE(offset)&0x7fffffff)%1000000).padStart(6,'0');
}
async function startLab(totp,port){
  const proc=spawn(path.resolve('build/wr-admin-lab'),['-port',String(port),'-setup-port',String(port+1),'-totp',totp],{env:{...process.env,WR_TEST_AUTH_DB:'local'},stdio:['ignore','pipe','pipe']});
  let tokenPath='';
  const ended=new Promise(resolve=>proc.once('exit',(code,signal)=>resolve({code,signal})));
  try{await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('lab startup timeout')),15000);
    proc.once('error',e=>{clearTimeout(timer);reject(e);});proc.once('exit',()=>{clearTimeout(timer);reject(Error('lab exited before ready'));});
    proc.stdout.on('data',chunk=>{output+=chunk;const match=output.match(/Bootstrap token file: (.+)\n/);if(match){tokenPath=match[1];clearTimeout(timer);resolve();}});
  });}catch(e){proc.kill('SIGTERM');await ended;throw e;}
  return {token:fs.readFileSync(tokenPath,'utf8'),async stop(){proc.kill('SIGTERM');let timer;try{const exit=await Promise.race([ended,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('lab shutdown timeout')),40000);})]);expect(exit.code).toBe(0);expect(fs.existsSync(tokenPath)).toBe(false);}finally{clearTimeout(timer);}}};
}
// No screenshot/trace of populated credential, QR or recovery screens.
for(const mode of ['on','off'])test(`real browser bootstrap ${mode}, session reload and logout`,async({page,context,browserName})=>{
  const port=mode==='on'?18453:18455,lab=await startLab(mode,port),origin=`https://127.0.0.1:${port}`,setup=`https://127.0.0.1:${port+1}`;
  const errors=[],remote=[],consoleErrors=[];
  page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404)/.test(m.text()))consoleErrors.push(m.text());});
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith('https://127.0.0.1:')&&!r.url().startsWith('data:'))remote.push(r.url());});
  try{
    // A previous disposable lab may have left an HttpOnly session cookie.
    await context.addCookies([{name:'__Host-wrs',value:'A'.repeat(43),url:origin,secure:true,httpOnly:true,sameSite:'Strict'}]);
    await page.goto(origin);await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);await expect(page).toHaveTitle('NudgeOn Waiting Room · 관리자 콘솔');
    expect((await context.cookies()).some(c=>c.name==='__Host-wrs')).toBe(false);
    await page.keyboard.press('Tab');await expect(page.getByLabel('사용자 이름')).toBeFocused();
    await page.getByRole('heading',{name:'관리자 로그인'}).click();
    if(process.env.WR_ADMIN_SCREEN_DIR&&mode==='on'){
      fs.mkdirSync(process.env.WR_ADMIN_SCREEN_DIR,{recursive:true});
      await capture(page,{path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'login-desktop.png')},consoleErrors,browserName);
      await page.setViewportSize({width:360,height:900});await expect(page.getByRole('button',{name:'로그인',exact:true})).toBeVisible();
      expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
      await capture(page,{path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'login-mobile.png'),fullPage:true},consoleErrors,browserName);await page.setViewportSize({width:1586,height:992});
    }
    const bootstrapDenied=await context.request.post(origin+'/api/admin/v1/bootstrap',{headers:{Origin:origin,'X-WR-Auth':'1','X-Bootstrap-Token':lab.token},data:{username:'blocked',password:'local browser fixture password'}});expect(bootstrapDenied.status()).toBe(404);
    await page.goto(setup+'/setup');await expect(page.getByRole('heading',{name:'첫 관리자 만들기'})).toBeVisible();
    await page.getByLabel('설치 토큰').fill(lab.token);await page.getByLabel('사용자 이름').fill('browser_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local browser fixture password 2026');
    await page.getByRole('button',{name:'관리자 만들기',exact:true}).click();
    let codes=[];
    if(mode==='on'){
      await expect(page.getByRole('heading',{name:'TOTP 등록',exact:true})).toBeVisible();await page.getByRole('button',{name:'등록 키 만들기'}).click();
      await expect(page.getByLabel('수동 등록 키',{exact:true})).toBeVisible();const key=await page.getByLabel('수동 등록 키',{exact:true}).inputValue();
      await expect(page.getByAltText('인증 앱에 등록할 QR 코드')).toBeVisible();expect(await page.getByAltText('인증 앱에 등록할 QR 코드').getAttribute('src')).toMatch(/^data:image\/png/);
      await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'등록 완료',exact:true}).click();
      await expect(page.getByRole('heading',{name:'복구 코드를 보관하세요'})).toBeVisible({timeout:30000});codes=await page.locator('.recovery-codes code').allTextContents();expect(new Set(codes).size).toBe(10);
      expect(await page.evaluate(()=>Object.keys(sessionStorage))).toEqual(['wr.admin.csrf.v1']);
      expect(await page.evaluate(()=>Object.keys(localStorage))).toEqual([]);
      await expect(page.getByRole('button',{name:'계속',exact:true})).toBeDisabled();await page.getByLabel('안전한 곳에 코드를 보관했습니다.').check();await page.getByRole('button',{name:'계속',exact:true}).click();
    }
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();await expect(page.getByText('browser_admin',{exact:true})).toBeVisible();
    const cookies=await context.cookies();const session=cookies.find(c=>c.name==='__Host-wrs');expect(session?.secure&&session.httpOnly&&session.sameSite==='Strict').toBe(true);
    await page.reload();await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();
    if(process.env.WR_ADMIN_SCREEN_DIR&&mode==='on')await capture(page,{path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'session-desktop.png')},consoleErrors,browserName);
    await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);expect((await context.cookies()).some(c=>c.name==='__Host-wrs')).toBe(false);
    // Public visitor cookies share the hostname across Gateway/Control ports.
    // Keep them through login, TOTP recovery, authenticated reads and logout.
    const visitorCookies=['__Host-wrq_fixture','__Host-wra_fixture'].map((name,index)=>({name,value:'visitor-fixture-'+index,url:origin,secure:true,httpOnly:true,sameSite:'Lax'}));
    await context.addCookies(visitorCookies);
    await page.goto(origin+'/auth/login');await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
    await page.getByLabel('사용자 이름').fill('browser_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local browser fixture password 2026');
    const [loginResponse]=await Promise.all([page.waitForResponse(r=>r.url()===origin+'/api/admin/v1/auth/login'&&r.request().method()==='POST'),page.getByRole('button',{name:'로그인',exact:true}).click()]);
    expect(loginResponse.status()).toBe(200);
    if(mode==='on'){
      await expect(page.getByRole('heading',{name:'인증 코드 확인'})).toBeVisible();await accessibility(page);await page.getByRole('button',{name:'복구 코드 사용'}).click();await page.getByLabel('복구 코드',{exact:true}).fill(codes[0]);await page.getByRole('button',{name:'확인',exact:true}).click();
    }
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);
    const retainedCookies=await context.cookies(origin);
    for(const cookie of visitorCookies)expect(retainedCookies.find(item=>item.name===cookie.name)?.value).toBe(cookie.value);
    expect(retainedCookies.some(cookie=>cookie.name==='__Host-wrs')).toBe(false);
    expect(errors).toEqual([]);expect(consoleErrors).toEqual([]);expect(remote).toEqual([]);
  }finally{try{await context.clearCookies();}finally{await lab.stop();}}
});

test('Room draft persists across reload, conflicts are explicit, and mobile form fits',async({page,context,browserName})=>{
  const lab=await startLab('off',18457),origin='https://127.0.0.1:18457',setup='https://127.0.0.1:18458';
  const errors=[],remote=[],consoleErrors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith('https://127.0.0.1:')&&!r.url().startsWith('data:'))remote.push(r.url());});
  page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404|412)/.test(m.text()))consoleErrors.push(m.text());});
  try{
    await page.goto(setup+'/setup');await page.getByLabel('설치 토큰').fill(lab.token);await page.getByLabel('사용자 이름').fill('draft_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local draft fixture password 2026');await page.getByRole('button',{name:'관리자 만들기',exact:true}).click();
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);
    await page.goto(origin);await page.getByLabel('사용자 이름').fill('draft_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local draft fixture password 2026');await page.getByRole('button',{name:'로그인',exact:true}).click();
    await page.getByRole('button',{name:'대기열 관리',exact:true}).click();await expect(page.getByRole('heading',{name:'대기열 관리'})).toBeFocused();await expect(page).toHaveTitle('NudgeOn Waiting Room · 관리자 콘솔');
    await expect(page.getByText('아직 대기열이 없습니다.',{exact:false})).toBeVisible();await page.getByRole('button',{name:'새 대기열 만들기',exact:true}).click();
    await page.getByLabel('보호할 페이지 (HTTPS)',{exact:true}).fill('https://origin.example.test/shop');
    await expect(page.getByLabel('서브도메인 사용 (기본)',{exact:true})).toBeChecked();
    await expect(page.getByLabel('방문자 접속 주소',{exact:true})).toHaveValue('waiting.origin.example.test');
    await page.getByLabel('다른 주소 직접 입력',{exact:true}).check();
    for(const [label,value] of [['대기열 ID','sale'],['표시 이름','가을 판매'],['방문자 접속 주소','shop.example.test'],['실제 서비스 주소 (HTTPS)','https://origin.example.test'],['서비스 상태 확인 주소','https://origin.example.test/health']])await page.getByLabel(label,{exact:true}).fill(value);
    await expect(page).toHaveURL(origin+'/rooms/new');
    await page.getByLabel('실제 서비스 주소 (HTTPS)',{exact:true}).fill('http://origin.example.test');await page.getByRole('button',{name:'다음 단계',exact:true}).click();await expect(page.getByLabel('실제 서비스 주소 (HTTPS)',{exact:true})).toBeFocused();await expect(page.getByLabel('실제 서비스 주소 (HTTPS)',{exact:true})).toHaveAttribute('aria-invalid','true');
    await page.getByLabel('실제 서비스 주소 (HTTPS)',{exact:true}).fill('https://origin.example.test');
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR){fs.mkdirSync(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,{recursive:true});await capture(page,{path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'wizard-connection.png'),fullPage:true},consoleErrors,browserName);}
    for(let i=0;i<3;i++)await page.getByRole('button',{name:'다음 단계',exact:true}).click();
    await expect(page.getByRole('heading',{name:'기다리는 순간에도 서비스답게'})).toBeFocused();
    await page.getByLabel('안내 제목',{exact:true}).fill('순서대로 입장합니다');await expect(page.getByRole('heading',{name:'순서대로 입장합니다'})).toBeVisible();
    await page.getByLabel('기본 색상 (HEX)',{exact:true}).fill('#336699');await expect(page.locator('.rw-waiting')).toHaveCSS('--room-accent','#336699');
    const logo=fs.readFileSync('test/fixtures/theme-logo.png');
    await page.getByLabel('로고 이미지 (선택)',{exact:true}).setInputFiles({name:'unsafe.svg',mimeType:'image/svg+xml',buffer:Buffer.from('<svg onload="alert(1)"/>')});await expect(page.getByRole('alert')).toContainText('PNG 또는 JPEG');
    await page.getByLabel('로고 이미지 (선택)',{exact:true}).setInputFiles({name:'logo.png',mimeType:'image/png',buffer:logo});await expect(page.getByAltText('선택한 로고 미리보기')).toBeVisible();await expect.poll(()=>page.getByAltText('선택한 로고 미리보기').evaluate(el=>el.complete&&el.naturalWidth===32)).toBe(true);await accessibility(page);await expect.poll(()=>page.locator('.skip-link').evaluate(el=>el.getBoundingClientRect().bottom<=0)).toBe(true);
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR){await capture(page,{path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'wizard-theme.png'),fullPage:true},consoleErrors,browserName);await page.setViewportSize({width:360,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await capture(page,{path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'wizard-mobile.png'),fullPage:true},consoleErrors,browserName);await page.setViewportSize({width:1586,height:992});}
    await page.getByRole('button',{name:'이전 단계',exact:true}).click();await expect(page.getByLabel('1분당 입장 허용 인원',{exact:true})).toHaveValue('600');await page.getByRole('button',{name:'다음 단계',exact:true}).click();await expect(page.getByLabel('안내 제목',{exact:true})).toHaveValue('순서대로 입장합니다');
    await page.getByRole('button',{name:'다음 단계',exact:true}).click();await expect(page.getByRole('heading',{name:'저장 전 검토'})).toBeVisible();
    await page.getByRole('button',{name:'설정 저장',exact:true}).click();await expect(page.getByRole('status').filter({hasText:'설정을 저장했습니다.'})).toContainText('설정을 저장했습니다.');await expect(page.locator('.audit-list li')).toHaveCount(1);
    await page.getByRole('button',{name:/가을 판매/}).click();await expect(page).toHaveURL(origin+'/rooms/sale/settings');await page.reload();await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('가을 판매');await expect(page.getByLabel('안내 제목',{exact:true})).toHaveValue('순서대로 입장합니다');await expect(page.getByAltText('선택한 로고 미리보기')).toBeVisible();await expect.poll(()=>page.getByAltText('선택한 로고 미리보기').evaluate(el=>el.complete&&el.naturalWidth===32)).toBe(true);
    await page.getByRole('navigation',{name:'대기열 화면'}).getByRole('link',{name:'입장 예약',exact:true}).click();await expect(page).toHaveURL(origin+'/rooms/sale/schedule');await expect(page.getByRole('heading',{name:'실제 운영 연결 필요'})).toBeVisible();await page.goBack();await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('가을 판매');await page.goForward();await expect(page).toHaveURL(origin+'/rooms/sale/schedule');await page.getByRole('navigation',{name:'대기열 화면'}).getByRole('link',{name:'설정',exact:true}).click();await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('가을 판매');
    const competing=await page.evaluate(async()=>{const r=await fetch('/api/admin/v1/config/draft');const c=await r.json();c.rooms[0].name='다른 운영자의 수정';return (await fetch('/api/admin/v1/config/draft',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':sessionStorage.getItem('wr.admin.csrf.v1'),'Idempotency-Key':crypto.randomUUID(),'If-Match':r.headers.get('ETag')},body:JSON.stringify(c)})).status;});expect(competing).toBe(200);
    await page.getByLabel('표시 이름',{exact:true}).fill('덮어쓰면 안 되는 수정');await page.getByRole('button',{name:'설정 저장',exact:true}).click();await expect(page.getByRole('alert')).toContainText('다른 운영자가 설정을 변경했습니다.');await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('덮어쓰면 안 되는 수정');
    await page.getByRole('button',{name:'저장된 설정 불러오기'}).click();await page.getByRole('button',{name:/다른 운영자의 수정/}).click();
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR){fs.mkdirSync(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,{recursive:true});await capture(page,{path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'room-desktop.png'),fullPage:true},consoleErrors,browserName);}
    await page.setViewportSize({width:360,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.getByLabel('1분당 입장 허용 인원',{exact:true}).fill('300');await page.getByRole('button',{name:'로고 제거',exact:true}).focus();await page.keyboard.press('Enter');await expect(page.getByAltText('선택한 로고 미리보기')).toHaveCount(0);await page.getByRole('button',{name:'설정 저장',exact:true}).click();await expect(page.getByRole('status').filter({hasText:'설정을 저장했습니다.'})).toContainText('설정을 저장했습니다.');
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR)await capture(page,{path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'room-mobile.png'),fullPage:true},consoleErrors,browserName);
    await page.getByRole('button',{name:'대시보드로'}).click();await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);
    await page.goto(origin+'/rooms/sale/settings');await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await accessibility(page);await expect(page.getByLabel('표시 이름',{exact:true})).toHaveCount(0);
    expect(errors).toEqual([]);expect(consoleErrors).toEqual([]);expect(remote).toEqual([]);
  }finally{try{await context.clearCookies();}finally{await lab.stop();}}
});
