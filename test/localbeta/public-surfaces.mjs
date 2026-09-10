// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import {expect} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import os from 'node:os';
import path from 'node:path';
import {publicSchema} from './public-contracts.mjs';

// Same real signed mode, two public representations. The browser intent is
// established before a fault, so a bootstrap shell cannot masquerade as a
// successfully queued visitor or conceal an unavailable Coordinator.
export async function publicSurfaceChecks({mode,docker,dataRequest,api,until,admissionToken,ticket,authTicket,room,browser,origin,withFaults}) {
  const prepared=await api('POST',`/_wr/v1/rooms/${room.publicId}/browser-prepare`,{target:'/shop/cart'},{Origin:origin});
  assert.equal(prepared.status,204);
  const intent=prepared.headers['set-cookie'].find(c=>c.startsWith('__Host-wri_')).split(';')[0];
  const admissionCookie=`__Host-wra_${room.publicId}=`;
  let checked=0;
  async function matrix(fault) {
    for(const surface of ['app','browser'])for(const credential of ['missing','valid-header','valid-cookie','invalid','ambiguous'])for(const method of surface==='app'?['GET','HEAD','POST','PUT','PATCH','DELETE']:['GET','HEAD']) {
      const headers=surface==='browser'?{Accept:'text/html',Cookie:intent}:{};
      if(credential==='valid-header'||credential==='ambiguous')headers['X-Waiting-Room-Admission']=admissionToken;
      if(credential==='invalid')headers['X-Waiting-Room-Admission']='invalid-admission';
      if(credential==='valid-cookie'||credential==='ambiguous')headers.Cookie=[headers.Cookie,admissionCookie+admissionToken].filter(Boolean).join('; ');
      let expected,code;
      const admitted=credential==='valid-header'||credential==='valid-cookie';
      if(mode==='OFF'||admitted) {expected=fault==='origin'?503:200;code='QUEUE_UNAVAILABLE';}
      else if(credential==='ambiguous') {expected=400;code='INVALID_REQUEST';}
      else if(mode==='DRAINING') {expected=503;code='QUEUE_DRAINING';}
      else if(surface==='app') {expected=429;code='WAITING_ROOM_REQUIRED';}
      else {expected=fault==='coordinator'?503:303;code='QUEUE_UNAVAILABLE';}
      const out=await dataRequest(method,'/shop/cart',['GET','HEAD'].includes(method)?undefined:{customer:'matrix'},headers);
      const label=`${mode}/${fault}/${surface}/${credential}/${method}`;
      assert.equal(out.status,expected,label);
      assert.equal(out.headers['cache-control'],'no-store',label+' cache');
      if(method==='HEAD')assert.equal(out.raw,'',label+' HEAD body');
      if(expected===200) {if(method!=='HEAD')assert.equal(out.body?.service,'protected-demo-origin',label+' origin');}
      else if(expected===303) assert.ok(out.headers.location?.startsWith('/_wr/wait/'),label+' waiting redirect; credentials suppressed');
      else if(surface==='browser'&&expected===503) {
        assert.ok(out.headers['content-type']?.startsWith('text/html'),label+' HTML error');
        assert.ok(out.headers['content-security-policy']?.includes("default-src 'none'"),label+' strict CSP');
        if(method!=='HEAD')assert.ok(out.raw.includes('다시 확인하기'),label+' retry action');
      } else if(method!=='HEAD') {publicSchema('Problem',out.body);assert.equal(out.body.code,code,label+' problem');}
      checked++;
    }
  }
  await matrix('healthy');
  if(withFaults)for(const [service,fault] of [['demo-origin','origin'],['coordinator','coordinator']]) {
    let stopped=false;
    try {
      docker('stop',service);stopped=true;await matrix(fault);
      if(fault==='coordinator') {
        for(const method of ['GET','HEAD']) {
          const out=await dataRequest(method,'/shop/cart',undefined,{Accept:'text/html',Cookie:`__Host-wrq_${room.publicId}=${ticket.ticketToken}`});
          assert.equal(out.status,mode==='OFF'?200:503,'existing queue navigation during '+mode+' Coordinator outage');
          if(mode!=='OFF')assert.ok(out.headers['content-type']?.startsWith('text/html'),'existing ticket HTML retry page');
          checked++;
        }
        if(mode==='HOLD') {
          const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:320,height:800}});
          try {
            const split=intent.indexOf('=');await context.addCookies([{name:intent.slice(0,split),value:intent.slice(split+1),url:origin,secure:true,httpOnly:true,sameSite:'Lax'}]);
            const page=await context.newPage();assert.equal((await page.goto(origin+'/shop/cart')).status(),503);
            await expect(page.getByRole('heading',{name:'잠시 입장을 멈췄어요'})).toBeVisible();
            await page.keyboard.press('Tab');await expect(page.getByRole('link',{name:'다시 확인하기'})).toBeFocused();
            assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
            const audit=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze();
            assert.deepEqual(audit.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)})),[]);
            await page.screenshot({path:path.join(os.tmpdir(),'wr-beta8-coordinator-down-mobile.png'),fullPage:true});
          } finally {await context.close();}
        }
      }
    } finally {if(stopped)docker('start',service);}
    await until(async()=> {
      if(service==='demo-origin')return (await dataRequest('GET','/shop/cart',undefined,{'X-Waiting-Room-Admission':admissionToken})).status===200;
      const out=await api('POST',`/_wr/v1/rooms/${room.publicId}/admissions`,undefined,authTicket);
      if(out.status===503)return false;
      assert.equal(out.status,200);assert.ok(out.body.admissionToken===admissionToken,'same admission after fault; credentials suppressed');return true;
    });
  }
  console.log(`PASS: ${mode} browser/app surface matrix ${checked} requests; faults=${Boolean(withFaults)}; missing/header/cookie/invalid/ambiguous credentials and HEAD boundaries`);
  return checked;
}
