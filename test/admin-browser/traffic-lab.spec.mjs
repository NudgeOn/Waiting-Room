// SPDX-License-Identifier: Apache-2.0
import {test,expect} from './fixtures.mjs';
import {spawn} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import Ajv2020 from 'ajv/dist/2020.js';
const api=JSON.parse(fs.readFileSync('api/openapi/admin-v1.yaml','utf8'));
const validateRun=new Ajv2020({strict:false,validateFormats:false}).compile({...api.components.schemas.TrafficLabRun,components:api.components});

async function startLab(){
 const child=spawn(path.resolve('build/wr-admin-lab'),['--traffic-lab','--totp','off','--port','18601','--setup-port','18602'],{env:{...process.env,WR_TEST_AUTH_DB:'local'},stdio:['ignore','pipe','ignore']});
 const ended=new Promise(resolve=>child.once('exit',resolve));let tokenPath;
 try{await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('traffic lab startup timeout')),15000);child.once('error',()=>{clearTimeout(timer);reject(Error('traffic lab failed'));});child.once('exit',()=>{clearTimeout(timer);reject(Error('traffic lab exited'));});child.stdout.on('data',chunk=>{output+=chunk;const match=output.match(/Bootstrap token file: (.+)\n/);if(match){tokenPath=match[1];clearTimeout(timer);resolve();}});});}catch(e){child.kill('SIGTERM');await ended;throw e;}
 return {token:fs.readFileSync(tokenPath,'utf8'),async stop(){child.kill('SIGTERM');expect(await ended).toBe(0);}};
}
test('Traffic Lab real Quick 20 and Smoke 1K, retained result, download and cancellation',async({page,context,browserName})=>{
 test.setTimeout(90000);const lab=await startLab(),origin='https://127.0.0.1:18601',errors=[],remote=[];
 page.on('pageerror',()=>errors.push('pageerror'));page.on('console',m=>{if(['error','warning'].includes(m.type())&&!/Failed to load resource: the server responded with a status of (401|404)/.test(m.text()))errors.push(m.text());});
 page.on('request',r=>{if(!r.url().startsWith(origin)&&!r.url().startsWith('data:'))remote.push('non-admin browser request');});
 try{
  const response=await context.request.post('https://127.0.0.1:18602/api/admin/v1/bootstrap',{headers:{Origin:'https://127.0.0.1:18602','X-WR-Auth':'1','X-Bootstrap-Token':lab.token},data:{username:'traffic_browser',password:crypto.randomBytes(32).toString('base64url')}});expect(response.status()).toBe(200);const grant=await response.json();await context.addInitScript(value=>sessionStorage.setItem('wr.admin.csrf.v1',value),grant.csrfToken);
  await page.goto(origin+'/traffic-lab');await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');await expect(page.getByRole('heading',{name:'Traffic Lab',exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'Quick 20 실행',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Quick 20 실행',exact:true}).click();
  await expect(page.locator('.tl-result .tl-status')).toHaveText('통과',{timeout:30000});await expect(page.getByRole('heading',{name:'Quick 20 결과',exact:true})).toBeVisible();await expect(page.locator('.tl-timeline tbody tr')).toHaveCount(20);
  await expect(page.locator('.tl-metrics div').filter({has:page.getByText('입장 완료',{exact:true})}).locator('dd')).toHaveText('3');
  const download=page.waitForEvent('download');await page.getByRole('button',{name:'결과 JSON 다운로드'}).click();const file=await download;expect(file.suggestedFilename()).toBe('waiting-room-traffic-lab-result.json');const result=JSON.parse(fs.readFileSync(await file.path(),'utf8'));expect(validateRun(result),JSON.stringify(validateRun.errors)).toBe(true);expect(result.report.queued).toBe(17);expect(result.report.checks).toHaveLength(7);
  if(process.env.WR_TRAFFIC_SCREEN_DIR&&browserName==='chromium'){
   fs.mkdirSync(process.env.WR_TRAFFIC_SCREEN_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.WR_TRAFFIC_SCREEN_DIR,'traffic-lab-desktop.png')});
   await page.setViewportSize({width:360,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:path.join(process.env.WR_TRAFFIC_SCREEN_DIR,'traffic-lab-mobile.png'),fullPage:true});await page.setViewportSize({width:1586,height:992});
  }
  await page.reload();await expect(page.locator('.tl-result .tl-status')).toHaveText('통과');await expect(page.getByRole('heading',{name:'Quick 20 결과',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Smoke 1K 실행',exact:true}).click();await expect(page.getByRole('heading',{name:'Smoke 1K 결과',exact:true})).toBeVisible();await expect(page.locator('.tl-result .tl-status')).toHaveText('통과',{timeout:30000});
  const latest=await context.request.get(origin+'/api/admin/v1/lab/runs');const runs=(await latest.json()).items;expect(runs).toHaveLength(2);expect(runs[0].report.joined).toBe(1000);expect(runs[0].report.queued).toBe(997);expect(runs[0].report.unexpectedErrors).toBe(0);
  // Pending cancellation is durable and cannot mint a second run on retry.
  const headers={Origin:origin,'X-CSRF-Token':grant.csrfToken,'Idempotency-Key':crypto.randomUUID()};const started=await context.request.post(origin+'/api/admin/v1/lab/runs',{headers,data:{preset:'smoke-1k'}});expect(started.status()).toBe(202);const run=await started.json();
  const cancel=await context.request.post(origin+'/api/admin/v1/lab/runs/'+run.id+'/cancel',{headers:{...headers,'Idempotency-Key':crypto.randomUUID()},data:{}});expect(cancel.status()).toBe(202);
  await expect.poll(async()=>{const response=await context.request.get(origin+'/api/admin/v1/lab/runs/'+run.id);return (await response.json()).state;}).toBe('cancelled');
  const replay=await context.request.post(origin+'/api/admin/v1/lab/runs',{headers,data:{preset:'smoke-1k'}});expect(replay.status()).toBe(202);expect(replay.headers()['idempotency-replayed']).toBe('true');expect((await replay.json()).id).toBe(run.id);
  await page.reload();await expect(page.locator('.tl-result .tl-status')).toHaveText('중지됨');await expect(page.getByRole('button',{name:'Quick 20 실행',exact:true})).toBeEnabled();
  expect(errors).toEqual([]);expect(remote).toEqual([]);
 }catch(error){const response=await context.request.get(origin+'/api/admin/v1/lab/runs');if(response.status()===200)console.log(JSON.stringify((await response.json()).items.map(run=>({state:run.state,report:run.report}))));throw error;}finally{await lab.stop();}
});
