// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import {spawn} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

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
for(const mode of ['on','off'])test(`real browser bootstrap ${mode}, session reload and logout`,async({page,context})=>{
  const port=mode==='on'?18453:18455,lab=await startLab(mode,port),origin=`https://127.0.0.1:${port}`,setup=`https://127.0.0.1:${port+1}`;
  const errors=[],remote=[],consoleErrors=[];
  page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404)/.test(m.text()))consoleErrors.push(m.text());});
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith('https://127.0.0.1:')&&!r.url().startsWith('data:'))remote.push(r.url());});
  try{
    // A previous disposable lab may have left an HttpOnly session cookie.
    await context.addCookies([{name:'__Host-wrs',value:'A'.repeat(43),url:origin,secure:true,httpOnly:true,sameSite:'Strict'}]);
    await page.goto(origin);await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');
    expect((await context.cookies()).some(c=>c.name==='__Host-wrs')).toBe(false);
    await page.keyboard.press('Tab');await expect(page.getByLabel('사용자 이름')).toBeFocused();
    await page.getByRole('heading',{name:'관리자 로그인'}).click();
    if(process.env.WR_ADMIN_SCREEN_DIR&&mode==='on'){
      fs.mkdirSync(process.env.WR_ADMIN_SCREEN_DIR,{recursive:true});
      await page.screenshot({path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'login-desktop.png')});
      await page.setViewportSize({width:360,height:900});await expect(page.getByRole('button',{name:'로그인',exact:true})).toBeVisible();
      expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
      await page.screenshot({path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'login-mobile.png'),fullPage:true});await page.setViewportSize({width:1586,height:992});
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
    if(process.env.WR_ADMIN_SCREEN_DIR&&mode==='on')await page.screenshot({path:path.join(process.env.WR_ADMIN_SCREEN_DIR,'session-desktop.png')});
    await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();expect((await context.cookies()).some(c=>c.name==='__Host-wrs')).toBe(false);
    await page.getByLabel('사용자 이름').fill('browser_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local browser fixture password 2026');await page.getByRole('button',{name:'로그인',exact:true}).click();
    if(mode==='on'){
      await expect(page.getByRole('heading',{name:'인증 코드 확인'})).toBeVisible();await page.getByRole('button',{name:'복구 코드 사용'}).click();await page.getByLabel('복구 코드',{exact:true}).fill(codes[0]);await page.getByRole('button',{name:'확인',exact:true}).click();
    }
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
    expect(errors).toEqual([]);expect(consoleErrors).toEqual([]);expect(remote).toEqual([]);
  }finally{try{await context.clearCookies();}finally{await lab.stop();}}
});

test('Room draft persists across reload, conflicts are explicit, and mobile form fits',async({page,context})=>{
  const lab=await startLab('off',18457),origin='https://127.0.0.1:18457',setup='https://127.0.0.1:18458';
  const errors=[],remote=[],consoleErrors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith('https://127.0.0.1:')&&!r.url().startsWith('data:'))remote.push(r.url());});
  page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404|412)/.test(m.text()))consoleErrors.push(m.text());});
  try{
    await page.goto(setup+'/setup');await page.getByLabel('설치 토큰').fill(lab.token);await page.getByLabel('사용자 이름').fill('draft_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local draft fixture password 2026');await page.getByRole('button',{name:'관리자 만들기',exact:true}).click();
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
    await page.goto(origin);await page.getByLabel('사용자 이름').fill('draft_admin');await page.getByLabel('비밀번호',{exact:true}).fill('local draft fixture password 2026');await page.getByRole('button',{name:'로그인',exact:true}).click();
    await page.getByRole('button',{name:'Room 초안 관리',exact:true}).click();await expect(page.getByRole('heading',{name:'Room 초안 관리'})).toBeFocused();await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');
    await expect(page.getByText('아직 Room이 없습니다.',{exact:false})).toBeVisible();await page.getByRole('button',{name:'새 Room 초안',exact:true}).click();
    for(const [label,value] of [['Room ID','sale'],['표시 이름','가을 판매'],['고객 호스트','shop.example.test'],['원본 HTTPS 주소','https://origin.example.test'],['원본 상태 확인 URL','https://origin.example.test/health']])await page.getByLabel(label,{exact:true}).fill(value);
    await page.getByLabel('안내 제목',{exact:true}).fill('순서대로 입장합니다');await expect(page.getByRole('heading',{name:'순서대로 입장합니다'})).toBeVisible();
    await page.getByRole('button',{name:'초안 저장',exact:true}).click();await expect(page.getByRole('status')).toContainText('초안을 저장했습니다.');await expect(page.locator('.audit-list li')).toHaveCount(1);
    await page.reload();await page.getByRole('button',{name:'Room 초안 관리',exact:true}).click();await page.getByRole('button',{name:/가을 판매/}).click();await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('가을 판매');await expect(page.getByLabel('안내 제목',{exact:true})).toHaveValue('순서대로 입장합니다');
    const competing=await page.evaluate(async()=>{const r=await fetch('/api/admin/v1/config/draft');const c=await r.json();c.rooms[0].name='다른 운영자의 수정';return (await fetch('/api/admin/v1/config/draft',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':sessionStorage.getItem('wr.admin.csrf.v1'),'Idempotency-Key':crypto.randomUUID(),'If-Match':r.headers.get('ETag')},body:JSON.stringify(c)})).status;});expect(competing).toBe(200);
    await page.getByLabel('표시 이름',{exact:true}).fill('덮어쓰면 안 되는 수정');await page.getByRole('button',{name:'초안 저장',exact:true}).click();await expect(page.getByRole('alert')).toContainText('다른 운영자가 설정을 변경했습니다.');await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('덮어쓰면 안 되는 수정');
    await page.getByRole('button',{name:'최신 초안 불러오기'}).click();await page.getByRole('button',{name:/다른 운영자의 수정/}).click();
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR){fs.mkdirSync(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'room-desktop.png'),fullPage:true});}
    await page.setViewportSize({width:360,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.getByLabel('분당 신규 입장 수',{exact:true}).fill('300');await page.getByRole('button',{name:'초안 저장',exact:true}).click();await expect(page.getByRole('status')).toContainText('초안을 저장했습니다.');
    if(process.env.WR_ADMIN_DRAFT_SCREEN_DIR)await page.screenshot({path:path.join(process.env.WR_ADMIN_DRAFT_SCREEN_DIR,'room-mobile.png'),fullPage:true});
    await page.getByRole('button',{name:'세션으로 돌아가기'}).click();await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
    expect(errors).toEqual([]);expect(consoleErrors).toEqual([]);expect(remote).toEqual([]);
  }finally{try{await context.clearCookies();}finally{await lab.stop();}}
});
