// SPDX-License-Identifier: Apache-2.0
import {accessibility} from './accessibility.mjs';
import {test,expect} from './fixtures.mjs';
import {spawn} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

async function lab(port){
  const child=spawn(path.resolve('build/wr-admin-lab'),['--setup-wizard','--port',String(port),'--setup-port',String(port+1)],{env:{...process.env,WR_TEST_AUTH_DB:'local'},stdio:['ignore','pipe','ignore']});
  const ended=new Promise(resolve=>child.once('exit',resolve));let tokenPath;
  try{await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('setup lab startup timeout')),15000);child.once('error',()=>{clearTimeout(timer);reject(Error('setup lab start failed'));});child.once('exit',()=>{clearTimeout(timer);reject(Error('setup lab exited'));});child.stdout.on('data',chunk=>{output+=chunk;const match=output.match(/Bootstrap token file: (.+)\n/);if(match){tokenPath=match[1];clearTimeout(timer);resolve();}});});}catch(e){child.kill('SIGTERM');await ended;throw e;}
  return {token:fs.readFileSync(tokenPath,'utf8'),async stop(){child.kill('SIGTERM');expect(await ended).toBe(0);}};
}
function otp(key){let bits='';for(const char of key)bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char).toString(2).padStart(5,'0');const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const mac=crypto.createHmac('sha1',Buffer.from(bits.match(/.{8}/g).map(v=>parseInt(v,2)))).update(counter).digest(),offset=mac.at(-1)&15;return String((mac.readUInt32BE(offset)&0x7fffffff)%1000000).padStart(6,'0');}

for(const totp of [false,true])test(`setup wizard real calibration, apply and ${totp?'TOTP':'password'} enrollment`,async({page,context,browserName})=>{
  test.setTimeout(120000);
  const port=totp?18573:18571,server=await lab(port),origin=`https://127.0.0.1:${port}`,setup=`https://127.0.0.1:${port+1}`,errors=[];
  page.on('pageerror',()=>errors.push('pageerror'));page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404)/.test(m.text()))errors.push(m.text());});
  const headers={Origin:setup,'X-WR-Auth':'1','X-Bootstrap-Token':server.token};
  const password=crypto.randomBytes(32).toString('base64url');
  try{
    expect((await context.request.post(setup+'/api/admin/v1/bootstrap',{headers,data:{username:'wizard_admin',password}})).status()).toBe(412);
    expect((await context.request.post(origin+'/api/admin/v1/setup/inspect',{headers:{...headers,Origin:origin},data:{}})).status()).toBe(404);
    await page.goto(setup+'/setup');await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');await expect(page.getByRole('heading',{name:'설치 위자드',exact:true})).toBeVisible();await accessibility(page);
    await page.getByLabel('설치 토큰',{exact:true}).fill(server.token);await page.getByRole('button',{name:'설치 확인',exact:true}).click();
    await expect(page.getByRole('heading',{name:'설치 설정을 준비하세요'})).toBeVisible();await accessibility(page);
    const input={schemaVersion:1,profile:'standard-10k',regionId:'seoul-test',queuePolicy:'fifo',expectedPeakVisitors:420,limits:{maxActiveAdmissionLeases:32,admissionsPerMinute:17,admissionTtlSeconds:180},totp:{mode:'configurable',enabled:totp}};
    await page.getByLabel('저장한 계획 JSON 불러오기').setInputFiles({name:'plan.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify({payload:{plan:{input}}}))});
    await expect(page.getByLabel('리전 ID')).toHaveValue('seoul-test');await page.getByRole('button',{name:'환경 확인으로'}).click();
    await page.getByRole('button',{name:'로그인 성능 보정',exact:true}).click();await expect(page.getByText('보정 완료',{exact:true})).toBeVisible({timeout:30000});
    await page.getByRole('button',{name:'적용 계획 검토'}).click();await expect(page.getByRole('heading',{name:'적용할 설정을 확인하세요'})).toBeVisible();await accessibility(page);
    await expect(page.getByRole('button',{name:'설정 적용',exact:true})).toBeDisabled();
    expect(await page.evaluate(()=>Object.keys(sessionStorage))).toEqual([]);expect(await page.evaluate(()=>Object.keys(localStorage))).toEqual([]);
    if(process.env.WR_SETUP_SCREEN_DIR&&!totp&&browserName==='chromium'){
      fs.mkdirSync(process.env.WR_SETUP_SCREEN_DIR,{recursive:true});expect(errors).toEqual([]);
      await page.screenshot({path:path.join(process.env.WR_SETUP_SCREEN_DIR,'setup-review-desktop.png'),fullPage:true});
      await page.setViewportSize({width:360,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
      await page.screenshot({path:path.join(process.env.WR_SETUP_SCREEN_DIR,'setup-review-mobile.png'),fullPage:true});
    }
    await page.getByLabel('위 설정을 이 로컬 설치에 적용합니다.').check();await page.getByRole('button',{name:'설정 적용',exact:true}).click();await expect(page.getByText('설정 적용 완료',{exact:true})).toBeVisible();
    const inspected=await context.request.post(setup+'/api/admin/v1/setup/inspect',{headers,data:{}});expect(inspected.status()).toBe(200);const saved=(await inspected.json()).report;expect(saved.plan.input).toEqual(input);expect(saved.plan.calibration.targetMet).toBe(true);
    const replay=await context.request.post(setup+'/api/admin/v1/setup/apply',{headers,data:{input,planDigest:saved.planDigest,calibrationDigest:saved.calibrationDigest}});expect(replay.status()).toBe(200);expect(await replay.json()).toEqual(saved);
    // Reload after apply must resume at administrator creation without recalibration.
    await page.reload();await page.getByLabel('설치 토큰',{exact:true}).fill(server.token);await page.getByRole('button',{name:'설치 확인',exact:true}).click();await expect(page.getByRole('heading',{name:'첫 관리자 만들기'})).toBeVisible();await accessibility(page);
    const download=page.waitForEvent('download');await page.getByRole('button',{name:'설치 결과 JSON 다운로드'}).click();const file=await download;expect(file.suggestedFilename()).toBe('waiting-room-installation-result.json');expect(JSON.parse(fs.readFileSync(await file.path(),'utf8'))).toEqual(saved);
    await page.getByLabel('사용자 이름').fill('wizard_admin');await page.getByLabel('비밀번호',{exact:true}).fill(password);await page.getByRole('button',{name:'관리자 만들기',exact:true}).click();
    let recoveryCode='';
    if(totp){await expect(page.getByRole('heading',{name:'TOTP 등록',exact:true})).toBeVisible();await accessibility(page);await page.getByRole('button',{name:'등록 키 만들기'}).click();const key=await page.getByLabel('수동 등록 키',{exact:true}).inputValue();await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'등록 완료',exact:true}).click();await expect(page.getByRole('heading',{name:'복구 코드를 보관하세요'})).toBeVisible({timeout:30000});await accessibility(page);recoveryCode=await page.locator('.recovery-codes code').first().textContent();await page.getByLabel('안전한 곳에 코드를 보관했습니다.').check();await page.getByRole('button',{name:'계속',exact:true}).click();}
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible({timeout:30000});
    const installation=await context.request.get(origin+'/api/admin/v1/installation');expect(installation.status()).toBe(200);expect((await installation.json()).report).toEqual(saved);
    const config=await context.request.get(origin+'/api/admin/v1/config/draft');expect((await config.json()).regionId).toBe('seoul-test');
    await page.getByRole('button',{name:'설정 완료 · 관리자 콘솔로 이동'}).click();
    await expect(page).toHaveURL(origin+'/auth/login');await page.getByLabel('사용자 이름').fill('wizard_admin');await page.getByLabel('비밀번호',{exact:true}).fill(password);await page.getByRole('button',{name:'로그인',exact:true}).click();
    if(totp){await page.getByRole('button',{name:'복구 코드 사용',exact:true}).click();await page.getByLabel('복구 코드',{exact:true}).fill(recoveryCode);await page.getByRole('button',{name:'확인',exact:true}).click();}
    await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible({timeout:30000});
    await page.goto(origin+'/rooms/new');await expect(page.getByRole('heading',{name:'새 Room 만들기'})).toBeVisible();
    for(const [label,value] of [['표시 이름','설치 기본값 확인'],['Room ID','setup_room'],['고객 호스트','shop.example.test'],['원본 HTTPS 주소','https://origin.example.test'],['원본 상태 확인 URL','https://origin.example.test/health']])await page.getByLabel(label,{exact:true}).fill(value);
    await page.getByRole('button',{name:'다음 단계',exact:true}).click();await page.getByRole('button',{name:'다음 단계',exact:true}).click();await expect(page.getByLabel('분당 신규 입장 수',{exact:true})).toHaveValue('17');await expect(page.getByLabel('최대 활성 입장권 수',{exact:true})).toHaveValue('32');
    const fresh=await context.browser().newContext({ignoreHTTPSErrors:true});try{expect((await fresh.request.post(setup+'/api/admin/v1/setup/inspect',{headers,data:{}})).status()).toBe(401);}finally{await fresh.close();}
    expect(errors).toEqual([]);
  }finally{await server.stop();}
});
