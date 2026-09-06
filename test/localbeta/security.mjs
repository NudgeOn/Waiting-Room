// SPDX-License-Identifier: Apache-2.0
// Real UI + HTTP security lifecycle. Never screenshot or log secret-bearing
// enrollment, recovery-code or password-filled screens.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {expect} from '@playwright/test';
import {adminSchema} from './contracts.mjs';

function otp(key){let bits='';for(const c of key)bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=Buffer.from(bits.match(/.{8}/g).map(v=>parseInt(v,2))),counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const mac=crypto.createHmac('sha1',bytes).update(counter).digest(),offset=mac.at(-1)&15;return String((mac.readUInt32BE(offset)&0x7fffffff)%1000000).padStart(6,'0');}
async function nextCounter(counter){while(Math.floor(Date.now()/30000)<=counter)await delay(250);}
export async function securityChecks({page,browser,password,mark,screens}){
 const origin='https://127.0.0.1:19443',errors=[];page.on('pageerror',()=>errors.push('pageerror'));
 async function settings(){if(new URL(page.url()).pathname!=='/settings')await page.getByRole('navigation',{name:'콘솔 화면'}).getByRole('link',{name:'사용자 · 보안',exact:true}).click();await expect(page.getByRole('heading',{name:'사용자 · 보안 설정',exact:true})).toBeFocused();await expect(page.getByText('설치 전체:',{exact:false})).toBeVisible();}
 async function confirm(code){const dialog=page.getByRole('dialog');await expect(dialog).toBeVisible();await expect(dialog.getByLabel('현재 비밀번호')).toBeFocused();await dialog.getByLabel('현재 비밀번호').fill(password);if(code)await dialog.getByLabel('현재 인증 코드').fill(code);await dialog.getByRole('button',{name:'확인하고 실행',exact:true}).click();await expect(dialog).not.toBeVisible({timeout:20000});}
 await settings();const firstReauth=Date.now();
 const initial=await page.evaluate(async()=>({users:await(await fetch('/api/admin/v1/users')).json(),policy:await(await fetch('/api/admin/v1/security/totp')).json()}));for(const user of initial.users.users)adminSchema('User',user);adminSchema('TOTPPolicy',initial.policy);
 const adminItem=page.locator('.user-list>li').filter({has:page.getByRole('heading',{name:'docker_test_admin',exact:true})});await expect(adminItem.getByRole('button',{name:'계정 삭제',exact:true})).toBeDisabled();
 const newPassword=crypto.randomBytes(32).toString('base64url');
 await page.getByLabel('새 사용자 ID',{exact:true}).fill('beta_operator');await page.getByLabel('새 사용자 비밀번호',{exact:true}).fill(newPassword);await page.getByLabel('새 사용자 역할',{exact:true}).selectOption('operator');await page.getByRole('button',{name:'계정 생성 준비',exact:true}).click();await confirm();
 const item=page.locator('.user-list>li').filter({has:page.getByRole('heading',{name:'beta_operator',exact:true})});await expect(item).toBeVisible();
 const operator=await browser.newContext({ignoreHTTPSErrors:true});
 try{
  const login=await operator.request.post(origin+'/api/admin/v1/auth/login',{headers:{Origin:origin,'X-WR-Auth':'1'},data:{username:'beta_operator',password:newPassword}});assert.equal(login.status(),200);const grant=await login.json();assert.equal(grant.session.role,'operator');
  assert.equal((await operator.request.get(origin+'/api/admin/v1/users')).status(),403);
  await item.getByLabel('beta_operator 역할',{exact:true}).selectOption('viewer');await item.getByRole('button',{name:'계정 변경',exact:true}).click();await confirm();await expect(item.getByText('viewer · 활성 · TOTP 미등록',{exact:true})).toBeVisible();
  assert.equal((await operator.request.get(origin+'/api/admin/v1/auth/me')).status(),401);
  await item.getByRole('button',{name:'계정 삭제',exact:true}).click();await confirm();await expect(item).toHaveCount(0);
  mark('Admin reauth create/demote/delete, Operator users API denied, role change revokes target session, last Admin UI guard');
 }finally{await operator.close();}
 await page.setViewportSize({width:360,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(screens,'security-mobile-off.png'),fullPage:true});
 await page.getByRole('button',{name:'ON 전 현재 Admin TOTP 등록',exact:true}).click();
 // Empty confirmation only: no passwords or OTPs are visible in this screenshot.
 await page.screenshot({path:path.join(screens,'security-reauth-mobile.png'),fullPage:true});await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).not.toBeVisible();await expect(page.getByRole('button',{name:'ON 전 현재 Admin TOTP 등록',exact:true})).toBeFocused();
 await page.getByRole('button',{name:'ON 전 현재 Admin TOTP 등록',exact:true}).click();await confirm();
 await expect(page.getByRole('heading',{name:'TOTP 등록',exact:true})).toBeVisible();await page.getByRole('button',{name:'등록 키 만들기',exact:true}).click();const key=await page.getByLabel('수동 등록 키',{exact:true}).inputValue();const enrolledCounter=Math.floor(Date.now()/30000);
 await page.getByLabel('인증 코드',{exact:true}).fill(otp(key));await page.getByRole('button',{name:'등록 완료',exact:true}).click();await expect(page.locator('.recovery-codes li')).toHaveCount(10);await page.getByLabel('안전한 곳에 코드를 보관했습니다.').check();await page.getByRole('button',{name:'계속',exact:true}).click();
 await settings();await expect(page.getByRole('button',{name:'TOTP ON으로 변경',exact:true})).toBeVisible();mark('OFF-policy Admin enrollment verifies local TOTP and returns ten recovery codes without prematurely enabling global policy');
 // Exercise real time windows, never patch the credential counter or bypass the
 // shared production login limiter just to make the browser test pass.
 console.log('CHECK: wait for next TOTP and real account throttle window');await nextCounter(enrolledCounter);while(Date.now()<firstReauth+61000)await delay(250);
 const oldSession=await browser.newContext({ignoreHTTPSErrors:true});await oldSession.addCookies(await page.context().cookies());
 try{
  const before=await page.evaluate(()=>sessionStorage.getItem('wr.admin.csrf.v1'));
  await page.getByRole('button',{name:'TOTP ON으로 변경',exact:true}).click();const enabledCounter=Math.floor(Date.now()/30000);await confirm(otp(key));await expect(page.getByRole('button',{name:'TOTP OFF로 변경',exact:true})).toBeVisible();
  assert.notEqual(await page.evaluate(()=>sessionStorage.getItem('wr.admin.csrf.v1')),before);assert.equal((await oldSession.request.get(origin+'/api/admin/v1/auth/me')).status(),401);
  mark('OFF to ON rotates secure Admin cookie and CSRF, old session immediately rejected');
  await page.setViewportSize({width:1586,height:992});await page.screenshot({path:path.join(screens,'security-desktop-on.png'),fullPage:true});
  console.log('CHECK: wait for a fresh TOTP before disabling policy');await nextCounter(enabledCounter);await page.getByRole('button',{name:'TOTP OFF로 변경',exact:true}).click();await confirm(otp(key));await expect(page.getByRole('button',{name:'TOTP ON으로 변경',exact:true})).toBeVisible();mark('ON to OFF requires fresh password plus non-replayed TOTP and rotates session again');
 }finally{await oldSession.close();}
 await page.getByRole('button',{name:'초안으로 돌아가기',exact:true}).click();await page.getByRole('button',{name:'실시간 운영',exact:true}).click();await page.getByRole('button',{name:/Docker 재시작 검증/}).click();await page.getByRole('button',{name:'재인증 후 즉시 OFF',exact:true}).click();await confirm();
 await expect.poll(async()=>page.evaluate(async()=>{const d=await(await fetch('/api/admin/v1/config/delivery')).json();return {state:d.state,mode:d.runtimes[0].runtime.mode};}),{timeout:25000}).toEqual({state:'applied',mode:'OFF'});
 assert.deepEqual(errors,[]);mark('action-bound instant OFF reaches both runtime roles; no browser JavaScript errors');
}
