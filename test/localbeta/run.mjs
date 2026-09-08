// SPDX-License-Identifier: Apache-2.0
// Explicit isolated Docker test. Preserves stopped volumes for diagnostics.
// No traces, credential screenshots, password/OTP/token output or public calls.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawn,spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import {chromium,expect} from '@playwright/test';
import {sourceDigest} from '../../scripts/prd.mjs';
import {completeLocalSetupWizard} from './setup.mjs';
import {runtimeChecks} from './runtime.mjs';

if(process.env.WR_TEST_LOCAL_BETA!=='local')throw Error('WR_TEST_LOCAL_BETA=local required');
const project='waiting-room-local-beta-test-'+crypto.randomBytes(4).toString('hex');
const root=process.cwd(),startedAt=new Date().toISOString(),digest=sourceDigest(root);
const env={...process.env,WR_LOCAL_BETA_PROJECT:project,WR_LOCAL_BETA_SECRET_DIR:path.join(root,'.cache',project,'secrets')};
const compose=['compose','-p',project,'-f','deploy/compose/local-beta.yaml'];
const screens=fs.mkdtempSync(path.join(os.tmpdir(),'wr-docker-beta-'));
const checks=[];
function command(bin,args,{failure=false}={}){
  const r=spawnSync(bin,args,{cwd:root,env,encoding:'utf8',timeout:120000,maxBuffer:2*1024*1024});
  if(failure){assert.ok(r.status!==0&&r.status!==null,'negative command must exit nonzero');return '';}
  if(r.status!==0)throw Error('Local Docker test command failed: '+args.filter(x=>x!=='-T').slice(0,5).join(' '));
  return r.stdout.trim();
}
const lifecycle=(...args)=>command('node',['scripts/local-beta.mjs',...args]);
const docker=(...args)=>command('docker',[...compose,...args]);
const mark=name=>{checks.push({name,result:'PASS'});console.log('PASS: '+name);};
function otp(key){
  let bits='';for(const char of key)bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char).toString(2).padStart(5,'0');
  const bytes=Buffer.from(bits.match(/.{8}/g).map(v=>parseInt(v,2))),counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
  const mac=crypto.createHmac('sha1',bytes).update(counter).digest(),offset=mac.at(-1)&15;return String((mac.readUInt32BE(offset)&0x7fffffff)%1000000).padStart(6,'0');
}
let browser,tunnel,context,error;
try{
  lifecycle('init','on');lifecycle('up');lifecycle('bootstrap');
  const token=docker('exec','-T','control','/wr-control','token');assert.equal(token.length,43);
  const inspect=JSON.parse(command('docker',['inspect',project+'-control-1']))[0];
  assert.equal(inspect.Config.User,'65532:65532');assert.equal(inspect.HostConfig.ReadonlyRootfs,true);
  assert.equal(inspect.HostConfig.PortBindings['19443/tcp'][0].HostIp,'127.0.0.1');assert.equal(inspect.HostConfig.PortBindings['19444/tcp'],undefined);
  assert.equal(inspect.Mounts.find(m=>m.Destination==='/state').RW,false);
  assert.equal(inspect.Mounts.some(m=>m.Destination.includes('owner_password')),false);
  const privileges=docker('exec','-T','postgres','psql','-U','wr_owner','-d','waiting_room','-Atc',"SELECT has_schema_privilege('wr_runtime','waiting_room','CREATE'),has_table_privilege('wr_runtime','waiting_room.control_audit','UPDATE'),has_table_privilege('wr_runtime','waiting_room.control_audit','DELETE'),has_table_privilege('wr_runtime','waiting_room.schema_migrations','UPDATE'),has_table_privilege('wr_runtime','waiting_room.control_audit','INSERT')");
  assert.equal(privileges,'f|f|f|f|t');mark('fresh Docker init, restricted runtime role, loopback publish and read-only state');
  tunnel=spawn('node',['scripts/local-beta.mjs','setup'],{cwd:root,env,stdio:['ignore','pipe','pipe']});
  await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('setup tunnel timeout')),10000);tunnel.stdout.once('data',()=>{clearTimeout(timer);resolve();});tunnel.once('exit',()=>{clearTimeout(timer);reject(Error('setup tunnel exited'));});});
  browser=await chromium.launch({headless:true});context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1586,height:992}});
  const page=await context.newPage(),origin='https://127.0.0.1:19443',setup='https://127.0.0.1:19444',errors=[],remote=[],consoleErrors=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith('https://127.0.0.1:')&&!r.url().startsWith('data:'))remote.push(r.url());});
  page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404)/.test(m.text()))consoleErrors.push(m.text());});
  const denied=await context.request.post(origin+'/api/admin/v1/bootstrap',{headers:{Origin:origin,'X-WR-Auth':'1','X-Bootstrap-Token':token},data:{username:'blocked',password:'not used local negative test'}});assert.equal(denied.status(),404);
  const password=crypto.randomBytes(32).toString('base64url');
  await page.goto(setup+'/setup');await completeLocalSetupWizard(page,token);
  await page.getByLabel('사용자 이름').fill('docker_test_admin');await page.getByLabel('비밀번호',{exact:true}).fill(password);await page.getByRole('button',{name:'관리자 만들기',exact:true}).click();
  await expect(page.getByRole('heading',{name:'TOTP 등록',exact:true})).toBeVisible();await page.getByRole('button',{name:'등록 키 만들기'}).click();
  await expect(page.getByLabel('수동 등록 키',{exact:true})).toBeVisible();const key=await page.getByLabel('수동 등록 키',{exact:true}).inputValue();const enrolledCounter=Math.floor(Date.now()/30000);
  await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'등록 완료',exact:true}).click();
  await expect(page.getByRole('heading',{name:'복구 코드를 보관하세요'})).toBeVisible();await page.getByLabel('안전한 곳에 코드를 보관했습니다.').check();await page.getByRole('button',{name:'계속',exact:true}).click();
  await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();mark('loopback setup tunnel, first admin and encrypted TOTP enrollment');
  await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
  // A real fresh TOTP after the first enrollment avoids intentionally replaying
  // the same counter; the bound is less than 31 seconds.
  while(Math.floor(Date.now()/30000)<=enrolledCounter)await delay(250);
  await page.goto(origin);await page.getByLabel('사용자 이름').fill('docker_test_admin');await page.getByLabel('비밀번호',{exact:true}).fill(password);await page.getByRole('button',{name:'로그인',exact:true}).click();
  await expect(page.getByRole('heading',{name:'인증 코드 확인'})).toBeVisible();await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'확인',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();
  const loginCounter=Math.floor(Date.now()/30000);
  await page.getByRole('button',{name:'Room 초안 관리',exact:true}).click();await expect(page.getByRole('heading',{name:'Room 초안 관리'})).toBeFocused();await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');
  await page.getByRole('button',{name:'새 Room 초안',exact:true}).click();
  for(const [label,value] of [['Room ID','docker_sale'],['표시 이름','Docker 재시작 검증'],['고객 호스트','shop.example.test'],['원본 HTTPS 주소','https://origin.example.test'],['원본 상태 확인 URL','https://origin.example.test/health']])await page.getByLabel(label,{exact:true}).fill(value);
  for(let step=0;step<4;step++)await page.getByRole('button',{name:'다음 단계',exact:true}).click();
  await page.getByRole('button',{name:'초안 저장',exact:true}).click();await expect(page.getByRole('status')).toContainText('초안을 저장했습니다.');await expect(page.locator('.audit-list li').filter({hasText:'초안 저장 · 저장 완료'})).toHaveCount(1);
  const before=await page.evaluate(async()=>({config:await(await fetch('/api/admin/v1/config/draft')).json(),audit:await(await fetch('/api/admin/v1/audit-events')).json()}));
  for(const action of ['auth.bootstrap','auth.totp.enroll','auth.logout','auth.login'])assert.ok(before.audit.items.some(e=>e.action===action));
  for(const secret of [token,password,key])assert.ok(!JSON.stringify(before.audit).includes(secret));
  mark('authenticated Room draft save and durable audit');
  lifecycle('init','on');command('node',['scripts/local-beta.mjs','init','off'],{failure:true});
  command('node',['scripts/local-beta.mjs','bootstrap'],{failure:true});mark('repeat init preserves state; policy overwrite and second bootstrap refused');
  docker('restart','postgres','control');docker('up','-d','--wait','control');
  await page.reload();await expect(page).toHaveURL(origin+'/rooms/docker_sale/settings');await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('Docker 재시작 검증');
  const after=await page.evaluate(async()=>({config:await(await fetch('/api/admin/v1/config/draft')).json(),audit:await(await fetch('/api/admin/v1/audit-events')).json()}));assert.deepEqual(after,before);
  await page.getByRole('button',{name:/Docker 재시작 검증/}).click();await expect(page.getByLabel('표시 이름',{exact:true})).toHaveValue('Docker 재시작 검증');
  await page.screenshot({path:path.join(screens,'persistent-desktop.png'),fullPage:true});await page.setViewportSize({width:360,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.screenshot({path:path.join(screens,'persistent-mobile.png'),fullPage:true});
  mark('Postgres and Control restart preserves session, exact config and audit; 360px UI fits');
  await page.getByRole('button',{name:'세션으로 돌아가기'}).click();await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();
  while(Math.floor(Date.now()/30000)<=loginCounter)await delay(250);
  await page.getByLabel('사용자 이름').fill('docker_test_admin');await page.getByLabel('비밀번호',{exact:true}).fill(password);await page.getByRole('button',{name:'로그인',exact:true}).click();
  await expect(page.getByRole('heading',{name:'인증 코드 확인'})).toBeVisible();await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'확인',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();
  mark('fresh password plus stored TOTP decryption/login works after Docker restart');
  if(process.env.WR_TEST_LOCAL_RUNTIME==='1')await runtimeChecks({page,browser,mark,screens,docker});
  await page.getByRole('button',{name:'로그아웃',exact:true}).click();await expect(page.getByRole('heading',{name:'관리자 로그인'})).toBeVisible();assert.deepEqual(errors,[]);assert.deepEqual(consoleErrors,[]);assert.deepEqual(remote,[]);
  // Upgrade fixture: represent a policy that changed since initial `init on`.
  // The authenticated ON/OFF transition is verified separately in security.mjs.
  docker('stop','control','coordinator','gateway','demo-origin');
  docker('exec','-T','postgres','psql','-U','wr_owner','-d','waiting_room','-v','ON_ERROR_STOP=1','-c','UPDATE waiting_room.auth_policy SET totp_enabled=false,version=version+1 WHERE singleton');
  const snapshotSQL="SELECT jsonb_build_object('policy',(SELECT to_jsonb(p) FROM waiting_room.auth_policy p),'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM waiting_room.auth_accounts a),'config',(SELECT document FROM waiting_room.control_config),'binding',(SELECT key_binding FROM waiting_room.install_identity))";
  const upgradeBefore=docker('exec','-T','postgres','psql','-U','wr_owner','-d','waiting_room','-Atc',snapshotSQL);
  lifecycle('upgrade');lifecycle('upgrade');
  assert.equal(docker('exec','-T','postgres','psql','-U','wr_owner','-d','waiting_room','-Atc',snapshotSQL),upgradeBefore);
  command('node',['scripts/local-beta.mjs','init','on'],{failure:true});
  docker('up','-d','--wait','control');assert.equal((await context.request.get(origin+'/livez')).status(),204);
  mark('explicit repeated upgrade preserves changed OFF policy, accounts, draft and key binding; init cannot overwrite policy');
  assert.equal(sourceDigest(root),digest,'source changed during verification');mark('no page/console/remote-request errors; frozen source');
}catch(e){error=e;console.error('FAIL: local Docker browser verification after '+checks.length+' completed checks ('+(e.name==='AssertionError'?'assertion':'operation')+'). Raw request/error text suppressed because it may contain credentials.');console.error('Safe error category: '+(e.message?.match(/net::[A-Z_]+|Timeout [0-9]+ms exceeded/)?.[0]??e.name));console.error('Test location: '+(e.stack?.match(/runtime\.mjs:\d+:\d+/)?.[0]??'main'));process.exitCode=1;}
finally{
  if(context)await context.clearCookies();if(browser)await browser.close();
  if(tunnel){tunnel.kill('SIGTERM');await Promise.race([new Promise(r=>tunnel.once('exit',r)),delay(5000)]);}
  try{docker('down');}catch{process.exitCode=1;checks.push({name:'release isolated test containers and networks; retain volumes',result:'FAIL'});}
  const report={kind:process.env.WR_TEST_LOCAL_RUNTIME==='1'?'local-docker-runtime-e2e':'local-docker-control-preview',betaDecision:'NO-GO',project,sourceDigest:digest,startedAt,finishedAt:new Date().toISOString(),result:process.exitCode?'FAIL':'PASS',checks,screenshots:screens,limitations:['not complete M3/Beta acceptance','isolated test containers/networks removed; volumes/secrets retained; credentials not recorded']};
  fs.mkdirSync('docs/evidence',{recursive:true});fs.writeFileSync('docs/evidence/'+project+'.json',JSON.stringify(report,null,2)+'\n');
  console.log('Evidence: docs/evidence/'+project+'.json');console.log('Screenshots: '+screens);
}
