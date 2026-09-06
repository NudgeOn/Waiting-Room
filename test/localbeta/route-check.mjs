// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import path from 'node:path';
import {expect} from '@playwright/test';
import {adminSchema} from './contracts.mjs';

export async function routeChecks({page,mark,screens}){
 const origin=new URL(page.url()).origin,external=[];
 const observe=r=>{if(!r.url().startsWith(origin+'/'))external.push('external request');};page.on('request',observe);
 try{
  await expect(page.getByRole('heading',{name:'URL 경로 판정',exact:true})).toBeVisible();
  const form=page.locator('form').filter({has:page.getByRole('button',{name:'URL 판정',exact:true})});
  const result=page.locator('.route-result');
  for(const [url,decision,label] of [
   ['https://127.0.0.1:20443/shop/cart?source=fixture','protected','경로상 보호 대상'],
   ['https://127.0.0.1:20443/shop/assets/logo.png','excluded','제외 경로'],
   ['https://127.0.0.1:20443/shopping','unprotected','일치하는 활성 보호 경로 없음'],
   ['https://169.254.169.254/latest/meta-data','unprotected','일치하는 활성 보호 경로 없음'],
   ['https://127.0.0.1:20443/_wr/v1','reserved','내부 전용 경로'],
   ['https://127.0.0.1:20443/%73hop','invalid','지원하지 않는 URL 형식'],
   ['https://127.0.0.1:20443/shop/../admin','invalid','지원하지 않는 URL 형식'],
  ]){
   console.log('CHECK: route diagnosis '+decision);
   await form.getByLabel('전체 HTTPS URL',{exact:true}).fill(url);await expect(result).toHaveCount(0);
   const [response]=await Promise.all([page.waitForResponse(r=>r.url()===origin+'/api/admin/v1/config/route-check'),form.getByRole('button',{name:'URL 판정',exact:true}).click()]);
   console.log('CHECK: route response '+response.status());assert.equal(response.status(),200);const out=await response.json();adminSchema('RouteCheckResult',out);assert.equal(out.match.decision,decision);assert.equal(out.source,'published');assert.equal(out.scope,'configuration-only');assert.equal(Object.hasOwn(out,'url'),false);
   await expect(result.getByRole('heading',{name:label,exact:true})).toBeVisible();
  }
  await form.getByRole('combobox',{name:'검사 기준',exact:true}).selectOption('draft');await expect(result).toHaveCount(0);await form.getByLabel('전체 HTTPS URL',{exact:true}).fill('https://127.0.0.1:20443/shop/cart');
  const [response]=await Promise.all([page.waitForResponse(r=>r.url()===origin+'/api/admin/v1/config/route-check'),form.getByRole('button',{name:'URL 판정',exact:true}).click()]);
  const out=await response.json();adminSchema('RouteCheckResult',out);assert.equal(out.source,'draft');assert.equal(out.generation,0);assert.equal(Object.hasOwn(out,'mode'),false);
  await expect(result).toContainText('저장 초안');await expect(result.getByRole('heading',{name:'경로상 보호 대상',exact:true})).toBeVisible();
  await page.screenshot({path:path.join(screens,'route-check-desktop.png'),fullPage:true});await page.setViewportSize({width:360,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(screens,'route-check-mobile.png'),fullPage:true});
  await page.setViewportSize({width:1586,height:992});assert.deepEqual(external,[]);
  mark('Docker URL diagnosis: protected/excluded/unprotected/reserved/invalid, saved draft vs published metadata, edited input clears stale result, zero target network requests, 360px fits');
 }catch(e){await page.screenshot({path:path.join(screens,'route-check-failure.png'),fullPage:true}).catch(()=>{});console.error('Route check failure: '+String(e.message).split('\n')[0]);throw e;}finally{page.off('request',observe);}
}
