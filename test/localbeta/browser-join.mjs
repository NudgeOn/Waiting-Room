// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import {expect} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {publicSchema} from './public-contracts.mjs';
import os from 'node:os';
import path from 'node:path';

// Native HTTPS requests have no browser cookie jar. A dropped response here
// really discards Set-Cookie, unlike Playwright's context-bound API client.
export async function browserJoinChecks({browser,origin,room,dataRequest,api}) {
 const queue='__Host-wrq_'+room, intent='__Host-wri_'+room;
 const prepare=`/_wr/v1/rooms/${room}/browser-prepare`;
 for (const [method,status,body,headers] of [
  ['GET',405,undefined,{}],
  ['POST',403,{target:'/shop'},{}],
  ['POST',403,{target:'/shop'},{Origin:'https://evil.test'}],
  ['POST',428,{target:'/shop',confirm:true},{Origin:origin}],
 ]) {
  const out=await (method==='POST'?api:dataRequest)(method,prepare,body,headers);
  assert.equal(out.status,status);publicSchema('Problem',out.body);
  assert.equal(out.headers['cache-control'],'no-store');
  if(method==='GET')assert.equal(out.headers.allow,'POST');
 }
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:360,height:900}});
 try {
  const page=await context.newPage();let lost=false,token='';
  const prefix='/shop/lost-response?',target=prefix+'&'.repeat(2048-prefix.length);
  await page.route(origin+target,async route=>{
   if(!lost&&!route.request().isNavigationRequest()){
    const cookies=(await context.cookies()).map(c=>c.name+'='+c.value).join('; ');
    const out=await dataRequest('GET',target,undefined,{Accept:'text/html',Cookie:cookies});
    assert.equal(out.status,303);const raw=out.headers['set-cookie'].find(c=>c.startsWith(queue+'='));assert.ok(raw);
    token=raw.split(';')[0].slice(queue.length+1);lost=true;await route.abort('failed');return;
   }
   await route.fallback();
  });
  await page.goto(origin+target);
  await expect(page.getByRole('button',{name:'다시 연결하기'})).toBeFocused();
  assert.equal((await context.cookies()).some(c=>c.name===queue),false);
  const prepared=(await context.cookies()).find(c=>c.name===intent);assert.ok(prepared.secure&&prepared.httpOnly&&prepared.sameSite==='Lax');
  assert.deepEqual((await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze()).violations.map(v=>v.id),[]);
  await page.screenshot({path:path.join(os.tmpdir(),'wr-beta4-https-join-retry.png'),fullPage:true});
  await page.keyboard.press('Enter');await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  await expect(page.locator('body')).toHaveAttribute('data-target',target);
  await page.reload();await expect(page.locator('body')).toHaveAttribute('data-target',target);
  const received=(await context.cookies()).find(c=>c.name===queue);assert.ok(received.value===token,'retry retains the original credential');assert.ok(received.secure&&received.httpOnly);
  assert.equal(await page.evaluate(()=>document.cookie.includes('__Host-wr')),false);
 } finally {await context.close();}
 const tabs=await browser.newContext({ignoreHTTPSErrors:true});
 try {
  const pages=await Promise.all(Array.from({length:5},()=>tabs.newPage()));
  await Promise.all(pages.map((page,i)=>page.goto(origin+'/shop/tabs?i='+i)));
  for(let i=0;i<pages.length;i++){
   await expect(pages[i].locator('body')).toHaveAttribute('data-state','queued');
   await expect(pages[i].locator('body')).toHaveAttribute('data-target','/shop/tabs?i='+i);
   assert.equal(await pages[i].evaluate(async()=>{const b=document.body;return (await fetch(b.dataset.heartbeatUrl,{method:'POST',headers:{'X-Waiting-Room-CSRF':b.dataset.return}})).status;}),204);
  }
 } finally {await tabs.close();}
 console.log('PASS: actual HTTPS maximum 2048-byte query, first browser response discarded with all cookies, same-ticket keyboard recovery, Secure/HttpOnly intent, 5 simultaneous fresh tabs with independent targets, prepare contracts and mobile axe');
}
