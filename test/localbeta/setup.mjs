// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import {expect} from '@playwright/test';

export async function applyLocalSetup(request,origin,token){
  const headers={Origin:origin,'X-WR-Auth':'1','X-Bootstrap-Token':token};
  async function post(step,data){const response=await request.post(origin+'/api/admin/v1/setup/'+step,{headers,data,timeout:30000});assert.equal(response.status(),200,'setup '+step);return response.json();}
  const inspection=await post('inspect',{});if(inspection.report)return inspection.report;
  const measured=await post('calibrate',{});assert.equal(measured.calibration.targetMet,true,'Control-host calibration target');
  const review=await post('plan',inspection.input);
  return post('apply',{input:inspection.input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});
}
export async function completeLocalSetupWizard(page,token){
  await expect(page.getByRole('heading',{name:'설치 위자드',exact:true})).toBeVisible();
  await page.getByLabel('설치 토큰',{exact:true}).fill(token);await page.getByRole('button',{name:'설치 확인',exact:true}).click();
  await page.getByRole('button',{name:'환경 확인으로'}).click();await page.getByRole('button',{name:'로그인 성능 보정',exact:true}).click();
  await expect(page.getByText('보정 완료',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'적용 계획 검토'}).click();
  await page.getByLabel('위 설정을 이 로컬 설치에 적용합니다.').check();await page.getByRole('button',{name:'설정 적용',exact:true}).click();
  await expect(page.getByRole('heading',{name:'첫 관리자 만들기'})).toBeVisible();
}
