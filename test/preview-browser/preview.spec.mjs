// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
const origin='http://127.0.0.1:18770';
async function exportFile(page,label){
  const promise=page.waitForEvent('download');await page.getByRole('button',{name:label,exact:true}).click();const download=await promise;
  expect(download.suggestedFilename()).toBe('waiting-room-planning-report.json');
  const text=fs.readFileSync(await download.path(),'utf8'),report=JSON.parse(text);
  const canonical=JSON.stringify(report.payload).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));
  expect(report.sha256).toBe(crypto.createHash('sha256').update(canonical).digest('hex'));
  expect(report.payload.installation).toBe('NOT_RUN');expect(report.payload.activationAllowed).toBe(false);expect(report.payload.qualification).toBe('NOT_RUN');
  return {text,report};
}
for(const mobile of [false,true])test(`planning preview real Go API ${mobile?'mobile':'desktop'}`,async({page})=>{
  if(mobile)await page.setViewportSize({width:360,height:900});
  const errors=[],external=[];let planCalls=0;
  page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(['error','warning'].includes(m.type()))errors.push(m.text());});
  page.on('request',r=>{if(!r.url().startsWith(origin)&&!r.url().startsWith('blob:'))external.push(r.url());if(r.url().endsWith('/preview/plan'))planCalls++;});
  await page.goto(origin+'/install-preview');await expect(page).toHaveTitle('Waiting Room · 설치 계획 미리보기');await expect(page.getByRole('heading',{name:'프로필과 보안',exact:true})).toBeVisible();
  await expect(page).toHaveURL(origin+'/install-preview');
  expect(await page.locator('body').innerText()).toContain('실제 설치 없음');
  expect(await page.locator('vite-error-overlay,[data-nextjs-dialog],#webpack-dev-server-client-overlay').count()).toBe(0);
  expect((await page.getByRole('option').allTextContents()).join(' ')).not.toMatch(/multi.?region|lottery|priority|weighted/i);
  await page.keyboard.press('Tab');await expect(page.getByLabel('설치 프로필')).toBeFocused();
  if(process.env.WR_PREVIEW_SCREEN_DIR){fs.mkdirSync(process.env.WR_PREVIEW_SCREEN_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.WR_PREVIEW_SCREEN_DIR,`wizard-${mobile?'mobile':'desktop'}.png`),fullPage:true});}
  await page.getByLabel('TOTP 사용').uncheck();
  await page.getByRole('button',{name:'계획 생성',exact:true}).click();
  await expect(page.getByRole('heading',{name:'계획 확인',exact:true})).toBeVisible();await expect(page.getByText('OFF / configurable',{exact:true})).toBeVisible();
  const planOnly=await exportFile(page,'계획 JSON 다운로드');expect(planOnly.report.payload.cost).toBeNull();expect(planOnly.report.payload.plan.input.totp.enabled).toBe(false);
  await page.getByRole('button',{name:'설정 수정',exact:true}).click();
  await page.getByLabel('예상 방문 상태 수').fill('10001');
  const before=planCalls;await page.getByRole('button',{name:'계획 생성',exact:true}).click();
  expect(await page.getByLabel('예상 방문 상태 수').evaluate(el=>el.validity.rangeOverflow)).toBe(true);expect(planCalls).toBe(before);
  await page.getByLabel('설치 프로필').selectOption('high-scale-100k');
  await page.getByLabel('예상 방문 상태 수').fill('100000');
  await page.getByLabel('TOTP 정책').selectOption('forced_on');await expect(page.getByLabel('TOTP 사용')).toBeChecked();await expect(page.getByLabel('TOTP 사용')).toBeDisabled();
  await page.getByRole('button',{name:'계획 생성',exact:true}).click();await expect(page.getByText('ON / forced_on',{exact:true})).toBeVisible();await expect(page.getByText('100,000',{exact:true})).toBeVisible();
  await page.getByRole('button',{name:'비용 비교로 이동',exact:true}).click();
  const fixture=JSON.parse(fs.readFileSync('test/installplan/cost-example.json'));
  for(const [label,key]of [['가격 제공자 (소문자 식별자)','provider'],['통화 코드','currency'],['단가 기준일','asOf'],['월 컴퓨팅 시간','monthlyHours'],['Standard 서버 시간당 단가','standardHostHourly'],['High 워커 1대 시간당 단가','highWorkerHourly'],['디스크 GiB당 월 단가','volumeGiBMonthly']])await page.getByLabel(label,{exact:true}).fill(String(fixture[key]));
  await page.getByRole('button',{name:'소계 계산',exact:true}).click();
  await expect(page.getByText('76.00 USD',{exact:true})).toBeVisible();await expect(page.getByText('888.00 USD',{exact:true})).toBeVisible();await expect(page.getByText('포함 소계 배율: 11.6842배',{exact:true})).toBeVisible();
  const exported=await exportFile(page,'계획·비용 JSON 다운로드');
  expect(exported.report.payload.cost.profiles[0].roundedSubtotal).toBe('76.00');expect(exported.report.payload.plan.input.profile).toBe('high-scale-100k');expect(exported.report.payload.cost.input.regionId).toBe(exported.report.payload.plan.input.regionId);
  expect((await exportFile(page,'계획·비용 JSON 다운로드')).text).toBe(exported.text);
  if(process.env.WR_PREVIEW_SCREEN_DIR&&!mobile)await page.screenshot({path:path.join(process.env.WR_PREVIEW_SCREEN_DIR,'wizard-cost.png'),fullPage:true});
  await expect(page.getByLabel('비용 계산 결과')).toBeVisible();
  await page.getByLabel('Standard 서버 시간당 단가',{exact:true}).fill('0');await expect(page.getByLabel('비용 계산 결과')).toHaveCount(0);
  await expect(page.getByRole('button',{name:'계획·비용 JSON 다운로드',exact:true})).toHaveCount(0);
  await page.getByLabel('디스크 GiB당 월 단가',{exact:true}).fill('0');await page.getByRole('button',{name:'소계 계산',exact:true}).click();await expect(page.getByText('포함 소계 배율: 계산 불가 (Standard 소계 0)',{exact:true})).toBeVisible();
  const changed=await exportFile(page,'계획·비용 JSON 다운로드');expect(changed.report.sha256).not.toBe(exported.report.sha256);expect(changed.report.payload.cost.profiles[0].roundedSubtotal).toBe('0.00');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  expect(await page.evaluate(()=>({local:Object.keys(localStorage),session:Object.keys(sessionStorage)}))).toEqual({local:[],session:[]});
  expect(errors).toEqual([]);expect(external).toEqual([]);
  await page.reload();await expect(page.getByRole('heading',{name:'프로필과 보안',exact:true})).toBeVisible();await expect(page.getByLabel('설치 프로필')).toHaveValue('standard-10k');
});

test('network failure leaves a retryable error without a result',async({page})=>{
  await page.goto(origin+'/install-preview');
  await page.route('**/preview/plan',route=>route.abort());
  await page.getByRole('button',{name:'계획 생성',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('서버에 연결할 수 없습니다');
  await expect(page.getByRole('button',{name:'계획 생성',exact:true})).toBeEnabled();
  await page.unroute('**/preview/plan');await page.getByRole('button',{name:'계획 생성',exact:true}).click();await expect(page.getByRole('heading',{name:'계획 확인',exact:true})).toBeVisible();
  await page.route('**/preview/report',route=>route.abort());await page.getByRole('button',{name:'계획 JSON 다운로드',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('서버에 연결할 수 없습니다');await expect(page.getByRole('button',{name:'계획 JSON 다운로드',exact:true})).toBeEnabled();
  await page.unroute('**/preview/report');expect((await exportFile(page,'계획 JSON 다운로드')).report.payload.cost).toBeNull();
});
