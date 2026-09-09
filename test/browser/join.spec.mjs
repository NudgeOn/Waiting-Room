// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const origin='http://127.0.0.1:18082',room='abcdefghijklmnopqrst';
const prepare=origin+`/_wr/v1/rooms/${room}/browser-prepare`;
const queue='wr_dev_q_'+room,intent='wr_dev_i_'+room;

test('first join response lost after commit retries the same ticket',async({page,context})=>{
 let dropped=false, originalToken='';
 await page.route(origin+'/shop/lost',async route=>{
  const request=route.request(),headers=await request.allHeaders();
  if(!dropped&&!request.isNavigationRequest()){
   // Node fetch has no cookie jar. Discard ALL upstream response headers and
   // body, including Set-Cookie, before the browser receives anything.
   const cookieHeader=(await context.cookies(request.url())).map(c=>c.name+'='+c.value).join('; ');
   const response=await fetch(request.url(),{headers:{...headers,cookie:cookieHeader},redirect:'manual'});
   expect(response.status).toBe(303);
   const cookie=response.headers.getSetCookie().find(c=>c.startsWith(queue+'='));
   expect(cookie).toBeTruthy();originalToken=cookie.split(';')[0].slice(queue.length+1);
   dropped=true;await response.arrayBuffer();await route.abort('failed');return;
  }
  await route.fallback();
 });
 await page.goto(origin+'/shop/lost');
 await expect(page.getByRole('button',{name:'다시 연결하기'})).toBeFocused();
 expect((await context.cookies()).some(c=>c.name===queue)).toBe(false);
 expect((await context.cookies()).some(c=>c.name===intent&&c.httpOnly)).toBe(true);
 await page.keyboard.press('Enter');
 await expect(page.locator('body')).toHaveAttribute('data-state','queued');
 expect((await context.cookies()).find(c=>c.name===queue).value===originalToken).toBe(true);
 expect(await page.evaluate(()=>document.cookie)).not.toContain('wr_dev_');
});

test('simultaneous cookie-less tabs share a ticket and keep each target',async({context})=>{
 const pages=await Promise.all(Array.from({length:5},()=>context.newPage()));
 await Promise.all(pages.map((page,i)=>page.goto(origin+'/shop/parallel?tab='+i)));
 for(const page of pages)await expect(page.locator('body')).toHaveAttribute('data-state','queued');
 const returns=new Set();for(let i=0;i<pages.length;i++){
  expect(await pages[i].locator('body').getAttribute('data-target')).toBe('/shop/parallel?tab='+i);
  returns.add(new URL(pages[i].url()).searchParams.get('return'));
  // Each tab's independently sealed return must authorize the SAME current
  // HttpOnly credential. This also works when WebKit hides Set-Cookie events.
  expect(await pages[i].evaluate(async()=>{const b=document.body;return (await fetch(b.dataset.heartbeatUrl,{method:'POST',headers:{'X-Waiting-Room-CSRF':b.dataset.return}})).status;})).toBe(204);
 }
 expect(returns.size).toBe(5);
});

test('lost prepare cookie stops before queue mutation and recovers by keyboard',async({page,context},testInfo)=>{
 await page.setViewportSize({width:360,height:900});
 let drop=true;
 await page.route(prepare,async route=>{
  const headers=await route.request().allHeaders();
  if(drop){
   const response=await fetch(prepare,{method:'POST',headers,body:route.request().postData(),redirect:'manual'});
   // Simulate a browser that rejects the intent cookie while receiving 204.
   if(response.status===204){await route.fulfill({status:204,headers:{'cache-control':'no-store'}});return;}
  }
  await route.fallback();
 });
 await page.goto(origin+'/shop/no-cookie');
 await expect(page.getByText('쿠키를 허용한 뒤 다시 연결해 주세요.')).toBeVisible();
 expect((await context.cookies()).some(c=>c.name===queue)).toBe(false);
 expect(await page.title()).toBe('Waiting Room');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 const audit=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
 expect(audit.violations.map(v=>v.id)).toEqual([]);
 await page.screenshot({path:'/private/tmp/wr-beta4-join-'+testInfo.project.name+'.png',fullPage:true});
 drop=false;await page.keyboard.press('Enter');
 await expect(page.locator('body')).toHaveAttribute('data-state','queued');
});
