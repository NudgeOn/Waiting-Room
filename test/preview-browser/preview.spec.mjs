// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

const origin='http://127.0.0.1:18770';
const steps=['규모 선택','운영 환경','유량 설정','관리자 보안','계획 확인','비용 비교'];
const priceFields=[['가격 제공자 (소문자 식별자)','provider'],['통화 코드','currency'],['단가 기준일','asOf'],['월 컴퓨팅 시간','monthlyHours'],['Standard 서버 시간당 단가','standardHostHourly'],['High 워커 1대 시간당 단가','highWorkerHourly'],['디스크 GiB당 월 단가','volumeGiBMonthly']];
const nav=(page,step)=>page.getByRole('button',{name:`${steps[step]} 단계로 이동`,exact:true});
async function atStep(page,step){
  const heading=page.getByRole('heading',{name:steps[step],exact:true});
  await expect(heading).toBeVisible();
  await expect(heading).toBeFocused();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
}
async function next(page,step){
  await page.getByRole('button',{name:step===4?'계획 생성':'다음 단계',exact:true}).click();
  await atStep(page,step);
}
async function back(page,step){
  await page.getByRole('button',{name:'이전 단계',exact:true}).click();
  await atStep(page,step);
}
async function screenshot(page,name){
  if(!process.env.WR_PREVIEW_SCREEN_DIR)return;
  fs.mkdirSync(process.env.WR_PREVIEW_SCREEN_DIR,{recursive:true});
  await page.screenshot({path:path.join(process.env.WR_PREVIEW_SCREEN_DIR,name),fullPage:true});
}
async function exportFile(page,label){
  const promise=page.waitForEvent('download');
  await page.getByRole('button',{name:label,exact:true}).click();
  const download=await promise;
  expect(download.suggestedFilename()).toBe('waiting-room-planning-report.json');
  const text=fs.readFileSync(await download.path(),'utf8'),report=JSON.parse(text);
  const canonical=JSON.stringify(report.payload).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));
  expect(report.sha256).toBe(crypto.createHash('sha256').update(canonical).digest('hex'));
  expect(report.payload.installation).toBe('NOT_RUN');
  expect(report.payload.activationAllowed).toBe(false);
  expect(report.payload.qualification).toBe('NOT_RUN');
  return {text,report};
}

for(const mobile of [false,true])test(`planning preview real Go API ${mobile?'mobile':'desktop'}`,async({page})=>{
  if(mobile)await page.setViewportSize({width:360,height:900});
  const errors=[],external=[];
  let planCalls=0;
  page.on('pageerror',e=>errors.push(e.message));
  page.on('console',m=>{if(['error','warning'].includes(m.type()))errors.push(m.text());});
  page.on('request',r=>{
    if(!r.url().startsWith(origin)&&!r.url().startsWith('blob:'))external.push(r.url());
    if(r.url().endsWith('/preview/plan'))planCalls++;
  });

  await page.goto(origin+'/install-preview');
  await expect(page).toHaveTitle('Waiting Room · 설치 계획 미리보기');
  await expect(page).toHaveURL(origin+'/install-preview');
  await atStep(page,0);
  expect(await page.locator('body').innerText()).toContain('실제 설치 없음');
  expect(await page.locator('vite-error-overlay,[data-nextjs-dialog],#webpack-dev-server-client-overlay').count()).toBe(0);
  expect((await page.getByRole('option').allTextContents()).join(' ')).not.toMatch(/multi.?region|lottery|priority|weighted/i);
  for(let step=1;step<steps.length;step++)await expect(nav(page,step)).toBeDisabled();
  await expect(page.getByLabel('설치 프로필')).toHaveValue('standard-10k');
  await screenshot(page,`wizard-${mobile?'mobile':'desktop'}.png`);

  // Each screen advances independently and returning to a screen preserves input.
  await next(page,1);
  await page.getByLabel('Home Region',{exact:true}).fill('eu-west-1');
  await next(page,2);
  await page.getByLabel('동시에 유지할 입장권 수').fill('1500');
  await page.getByLabel('1분당 입장 허용 인원').fill('750');
  await page.getByLabel('입장권 유효 시간 (초)',{exact:true}).fill('1200');
  await back(page,1);
  await expect(page.getByLabel('Home Region',{exact:true})).toHaveValue('eu-west-1');
  await next(page,2);
  await expect(page.getByLabel('동시에 유지할 입장권 수')).toHaveValue('1500');
  await expect(page.getByLabel('1분당 입장 허용 인원')).toHaveValue('750');
  await expect(page.getByLabel('입장권 유효 시간 (초)',{exact:true})).toHaveValue('1200');
  await next(page,3);
  expect(planCalls).toBe(0);
  await page.getByLabel('TOTP 사용').uncheck();
  await next(page,4);
  await expect(page.getByText('OFF / configurable',{exact:true})).toBeVisible();
  const planOnly=await exportFile(page,'계획 JSON 다운로드');
  expect(planOnly.report.payload.cost).toBeNull();
  expect(planOnly.report.payload.plan.input.totp.enabled).toBe(false);
  expect(planOnly.report.payload.plan.input.regionId).toBe('eu-west-1');
  expect(planOnly.report.payload.plan.input.limits).toEqual({maxActiveAdmissionLeases:1500,admissionsPerMinute:750,admissionTtlSeconds:1200});
  for(const label of ['규모 수정','환경 수정','유량 수정','보안 수정'])await expect(page.getByRole('button',{name:label,exact:true})).toBeVisible();

  // A review edit requires a new plan before either result screen can reopen.
  await page.getByRole('button',{name:'유량 수정',exact:true}).click();
  await atStep(page,2);
  await expect(page.getByLabel('1분당 입장 허용 인원')).toHaveValue('750');
  await page.getByLabel('1분당 입장 허용 인원').fill('800');
  await expect(nav(page,4)).toBeDisabled();
  await expect(nav(page,5)).toBeDisabled();
  await expect(page.getByRole('button',{name:'계획 JSON 다운로드',exact:true})).toHaveCount(0);
  await next(page,3);
  await expect(page.getByLabel('TOTP 사용')).not.toBeChecked();
  await next(page,4);
  expect((await exportFile(page,'계획 JSON 다운로드')).report.payload.plan.input.limits.admissionsPerMinute).toBe(800);

  await page.getByRole('button',{name:'설정 수정',exact:true}).click();
  await atStep(page,0);
  await next(page,1);
  await next(page,2);
  await page.getByLabel('예상 방문 상태 수').fill('10001');
  const beforeInvalid=planCalls;
  await page.getByRole('button',{name:'다음 단계',exact:true}).click();
  await expect(page.getByLabel('예상 방문 상태 수')).toBeFocused();
  await expect(page.getByRole('alert')).toBeVisible();
  expect(await page.getByLabel('예상 방문 상태 수').evaluate(el=>el.validity.rangeOverflow)).toBe(true);
  expect(planCalls).toBe(beforeInvalid);

  // Switching down a profile must retain, explain, and reject excess capacity.
  await nav(page,0).click();
  await atStep(page,0);
  await page.getByLabel('설치 프로필').selectOption('high-scale-100k');
  await next(page,1);
  await next(page,2);
  await page.getByLabel('예상 방문 상태 수').fill('100000');
  await nav(page,0).click();
  await atStep(page,0);
  await page.getByLabel('설치 프로필').selectOption('standard-10k');
  await next(page,1);
  await next(page,2);
  await expect(page.getByLabel('예상 방문 상태 수')).toHaveValue('100000');
  await page.getByRole('button',{name:'다음 단계',exact:true}).click();
  await expect(page.getByLabel('예상 방문 상태 수')).toBeFocused();
  await expect(page.getByRole('alert')).toBeVisible();
  expect(planCalls).toBe(beforeInvalid);
  await page.getByLabel('예상 방문 상태 수').fill('10000');
  await next(page,3);
  await nav(page,0).click();
  await atStep(page,0);
  await page.getByLabel('설치 프로필').selectOption('high-scale-100k');
  await next(page,1);
  await expect(page.getByLabel('Home Region',{exact:true})).toHaveValue('eu-west-1');
  await next(page,2);
  await expect(page.getByLabel('예상 방문 상태 수')).toHaveValue('10000');
  await page.getByLabel('예상 방문 상태 수').fill('100000');
  await expect(page.getByLabel('1분당 입장 허용 인원')).toHaveValue('800');
  await next(page,3);
  await page.getByLabel('TOTP 정책').selectOption('forced_on');
  await expect(page.getByLabel('TOTP 사용')).toBeChecked();
  await expect(page.getByLabel('TOTP 사용')).toBeDisabled();
  await next(page,4);
  await expect(page.getByText('ON / forced_on',{exact:true})).toBeVisible();
  await expect(page.getByText('100,000',{exact:true}).first()).toBeVisible();
  await screenshot(page,`wizard-review-${mobile?'mobile':'desktop'}.png`);
  await page.getByRole('button',{name:'비용 비교로 이동',exact:true}).click();
  await atStep(page,5);

  const fixture=JSON.parse(fs.readFileSync('test/installplan/cost-example.json'));
  for(const [label,key]of priceFields)await page.getByLabel(label,{exact:true}).fill(String(fixture[key]));
  await page.getByRole('button',{name:'소계 계산',exact:true}).click();
  await expect(page.getByText('76.00 USD',{exact:true})).toBeVisible();
  await expect(page.getByText('888.00 USD',{exact:true})).toBeVisible();
  await expect(page.getByText('포함 소계 배율: 11.6842배',{exact:true})).toBeVisible();
  const exported=await exportFile(page,'계획·비용 JSON 다운로드');
  expect(exported.report.payload.cost.profiles[0].roundedSubtotal).toBe('76.00');
  expect(exported.report.payload.plan.input.profile).toBe('high-scale-100k');
  expect(exported.report.payload.cost.input.regionId).toBe(exported.report.payload.plan.input.regionId);
  expect((await exportFile(page,'계획·비용 JSON 다운로드')).text).toBe(exported.text);
  await screenshot(page,`wizard-cost-${mobile?'mobile':'desktop'}.png`);

  await page.getByRole('button',{name:'계획으로 돌아가기',exact:true}).click();
  await atStep(page,4);
  await expect(page.getByText('ON / forced_on',{exact:true})).toBeVisible();
  await page.getByRole('button',{name:'비용 비교로 이동',exact:true}).click();
  await atStep(page,5);
  for(const [label,key]of priceFields)await expect(page.getByLabel(label,{exact:true})).toHaveValue(String(fixture[key]));
  await expect(page.getByLabel('비용 계산 결과')).toBeVisible();
  expect((await exportFile(page,'계획·비용 JSON 다운로드')).text).toBe(exported.text);

  await page.getByLabel('Standard 서버 시간당 단가',{exact:true}).fill('0');
  await expect(page.getByLabel('비용 계산 결과')).toHaveCount(0);
  await expect(page.getByRole('button',{name:'계획·비용 JSON 다운로드',exact:true})).toHaveCount(0);
  await page.getByLabel('디스크 GiB당 월 단가',{exact:true}).fill('0');
  await page.getByRole('button',{name:'소계 계산',exact:true}).click();
  await expect(page.getByText('포함 소계 배율: 계산 불가 (Standard 소계 0)',{exact:true})).toBeVisible();
  const changed=await exportFile(page,'계획·비용 JSON 다운로드');
  expect(changed.report.sha256).not.toBe(exported.report.sha256);
  expect(changed.report.payload.cost.profiles[0].roundedSubtotal).toBe('0.00');

  // Plan edits also invalidate an existing estimate, while entered prices survive.
  await page.getByRole('button',{name:'계획으로 돌아가기',exact:true}).click();
  await page.getByRole('button',{name:'환경 수정',exact:true}).click();
  await atStep(page,1);
  await page.getByLabel('Home Region',{exact:true}).fill('ap-northeast-2');
  await expect(nav(page,4)).toBeDisabled();
  await expect(nav(page,5)).toBeDisabled();
  await next(page,2);
  await next(page,3);
  await next(page,4);
  const revised=await exportFile(page,'계획 JSON 다운로드');
  expect(revised.report.payload.cost).toBeNull();
  expect(revised.report.payload.plan.input.regionId).toBe('ap-northeast-2');
  await page.getByRole('button',{name:'비용 비교로 이동',exact:true}).click();
  await atStep(page,5);
  await expect(page.getByLabel('가격 제공자 (소문자 식별자)',{exact:true})).toHaveValue(fixture.provider);
  await expect(page.getByLabel('Standard 서버 시간당 단가',{exact:true})).toHaveValue('0');
  await expect(page.getByLabel('디스크 GiB당 월 단가',{exact:true})).toHaveValue('0');
  await expect(page.getByLabel('비용 계산 결과')).toHaveCount(0);
  await expect(page.getByRole('button',{name:'계획·비용 JSON 다운로드',exact:true})).toHaveCount(0);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  expect(await page.evaluate(()=>({local:Object.keys(localStorage),session:Object.keys(sessionStorage)}))).toEqual({local:[],session:[]});
  expect(errors).toEqual([]);
  expect(external).toEqual([]);
  await page.reload();
  await atStep(page,0);
  await expect(page.getByLabel('설치 프로필')).toHaveValue('standard-10k');
  for(let step=1;step<steps.length;step++)await expect(nav(page,step)).toBeDisabled();
  await next(page,1);
  await next(page,2);
  await expect(page.getByLabel('예상 방문 상태 수')).toHaveValue('10000');
});

test('network failure leaves a retryable error without a result',async({page})=>{
  await page.goto(origin+'/install-preview');
  for(let step=1;step<=3;step++)await next(page,step);
  await page.route('**/preview/plan',route=>route.abort());
  await page.getByRole('button',{name:'계획 생성',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('서버에 연결할 수 없습니다');
  await expect(page.getByRole('button',{name:'계획 생성',exact:true})).toBeEnabled();
  await expect(page.getByRole('heading',{name:'관리자 보안',exact:true})).toBeVisible();
  await expect(nav(page,4)).toBeDisabled();
  await expect(nav(page,5)).toBeDisabled();
  await page.unroute('**/preview/plan');
  await next(page,4);
  await page.route('**/preview/report',route=>route.abort());
  await page.getByRole('button',{name:'계획 JSON 다운로드',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('서버에 연결할 수 없습니다');
  await expect(page.getByRole('button',{name:'계획 JSON 다운로드',exact:true})).toBeEnabled();
  await page.unroute('**/preview/report');
  expect((await exportFile(page,'계획 JSON 다운로드')).report.payload.cost).toBeNull();
});
