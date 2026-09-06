// SPDX-License-Identifier: Apache-2.0
// Disposable OFF-policy fixture for rapid data-plane diagnosis; full ON-policy
// persistence regression remains run.mjs. No saved user installation is touched.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawn,spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import {chromium,expect} from '@playwright/test';
import {newRoom} from '../../apps/admin/src/control-api.js';
import {runtimeChecks} from './runtime.mjs';
import {securityChecks} from './security.mjs';
import {sourceDigest} from '../../scripts/prd.mjs';
if(process.env.WR_TEST_LOCAL_BETA!=='local')throw Error('explicit local test consent required');
const project='waiting-room-local-beta-test-'+crypto.randomBytes(4).toString('hex'),root=process.cwd();
const env={...process.env,WR_LOCAL_BETA_PROJECT:project,WR_LOCAL_BETA_SECRET_DIR:path.join(root,'.cache',project,'secrets')};
const compose=['compose','-p',project,'-f','deploy/compose/local-beta.yaml'],checks=[],startedAt=new Date().toISOString(),digest=sourceDigest(root),screens=fs.mkdtempSync(path.join(os.tmpdir(),'wr-runtime-'));
function command(bin,args){const r=spawnSync(bin,args,{cwd:root,env,encoding:'utf8',timeout:120000,maxBuffer:2*1024*1024});if(r.status!==0)throw Error('command failed ('+bin+' exit '+r.status+')');return r.stdout.trim();}
const lifecycle=(...a)=>command('node',['scripts/local-beta.mjs',...a]),docker=(...a)=>command('docker',[...compose,...a]);
const mark=name=>{checks.push({name,result:'PASS'});console.log('PASS: '+name);};
let browser,tunnel;
try{
 console.log('CHECK: initialize '+project);lifecycle('init','off');console.log('CHECK: start roles');lifecycle('up');lifecycle('bootstrap');
 tunnel=spawn('node',['scripts/local-beta.mjs','setup'],{cwd:root,env,stdio:['ignore','pipe','ignore']});await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('tunnel timeout')),10000);tunnel.stdout.once('data',()=>{clearTimeout(timer);resolve();});});
 browser=await chromium.launch({headless:true});const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1586,height:992}});
 const token=docker('exec','-T','control','/wr-control','token');const setup='https://127.0.0.1:19444',admin='https://127.0.0.1:19443';
 const password=crypto.randomBytes(32).toString('base64url');const response=await context.request.post(setup+'/api/admin/v1/bootstrap',{headers:{Origin:setup,'X-WR-Auth':'1','X-Bootstrap-Token':token},data:{username:'docker_test_admin',password}});assert.equal(response.status(),200);const grant=await response.json();assert.equal(grant.state,'authenticated');
 await context.addInitScript(value=>sessionStorage.setItem('wr.admin.csrf.v1',value),grant.csrfToken);
 const room={...newRoom(),id:'docker_sale',name:'Docker 재시작 검증',hostname:'shop.example.test',origin:'https://origin.example.test',healthURL:'https://origin.example.test/health'};
 room.queuePolicy.readyTtlSeconds=60;
 const draft=await context.request.put(admin+'/api/admin/v1/config/draft',{headers:{Origin:admin,'X-CSRF-Token':grant.csrfToken,'If-Match':'"config-0"','Idempotency-Key':crypto.randomUUID()},data:{schemaVersion:1,revision:0,profile:'standard-10k',regionId:'local',rooms:[room]}});assert.equal(draft.status(),200);
 const page=await context.newPage(),browserErrors=[];page.on('pageerror',()=>browserErrors.push('pageerror'));page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|403|404|409|412)/.test(m.text()))browserErrors.push('unexpected console '+m.type());});await page.goto(admin);await expect(page.getByRole('heading',{name:'관리자 세션'})).toBeVisible();await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');
 await runtimeChecks({page,browser,mark,screens,docker});
 if(process.env.WR_TEST_LOCAL_RECOVERY==='1'){
  await page.getByRole('button',{name:'Room 초안 관리',exact:true}).click();await expect(page.getByRole('heading',{name:'최근 운영 감사 로그',exact:true})).toBeVisible();await expect(page.getByText('대기열 복구 · 안전 대기 시작',{exact:true})).toBeVisible();await expect(page.getByText('대기열 복구 · 검증 후 복구',{exact:true})).toBeVisible();assert.equal(await page.locator('.audit-list').innerText().then(t=>t.includes('Invalid Date')),false);
  const timestamps=await page.locator('.audit-list time').evaluateAll(nodes=>nodes.map(n=>({date:n.dateTime,text:n.textContent})));assert.ok(timestamps.length>0);assert.ok(timestamps.every(t=>Number.isFinite(Date.parse(t.date))&&!t.text.includes('확인 필요')));
  await page.screenshot({path:path.join(screens,'runtime-recovery-audit-mobile.png'),fullPage:true});await page.getByRole('button',{name:'세션으로 돌아가기',exact:true}).click();mark('recovery audit UI shows held/resumed outcomes and server occurrence timestamps');
 }
 if(process.env.WR_TEST_LOCAL_SECURITY==='1')await securityChecks({page,browser,password,mark,screens});assert.deepEqual(browserErrors,[]);mark('Admin runtime interactions have no JavaScript errors or unexpected console warnings');assert.equal(sourceDigest(root),digest);mark('runtime source unchanged');
}catch(e){console.error('FAIL after '+checks.length+' checks; category '+(e.message?.match(/net::[A-Z_]+|Timeout [0-9]+ms exceeded/)?.[0]??e.name)+'; location '+(e.stack?.match(/(?:runtime(?:-quick)?|security|recovery|events)\.mjs:\d+:\d+/)?.[0]??'unknown'));process.exitCode=1;}
finally{
 if(browser)await browser.close();if(tunnel){tunnel.kill('SIGTERM');await Promise.race([new Promise(r=>tunnel.once('exit',r)),delay(3000)]);}try{docker('down');}catch{process.exitCode=1;}
 const report={kind:'local-runtime-fast-off-fixture',project,sourceDigest:digest,startedAt,finishedAt:new Date().toISOString(),result:process.exitCode?'FAIL':'PASS',checks,screenshots:screens,betaDecision:'NO-GO'};
 fs.writeFileSync('docs/evidence/'+project+'.json',JSON.stringify(report,null,2)+'\n');console.log('Evidence: '+project+'; screenshots: '+screens);
}
