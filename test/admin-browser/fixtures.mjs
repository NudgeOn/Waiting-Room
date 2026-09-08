// SPDX-License-Identifier: Apache-2.0
import {test as base,expect} from '@playwright/test';
import {adminResponse} from '../localbeta/contracts.mjs';
export {expect};
export const test=base.extend({
 contractAudit:[async({page},use)=>{
  const errors=[];
  // Validate the actual server response before releasing it to the page. A
  // logout may immediately navigate and discard Chromium's response-body handle.
  // No response status/body/header is synthesized or replaced here.
  await page.route('**/api/admin/v1/**',async route=>{
   try{
    const response=await route.fetch();
    adminResponse(route.request().method(),route.request().url(),response.status(),response.headers(),await response.json());
    await route.fulfill({response});
   }catch(error){errors.push(error instanceof Error&&error.name==='AssertionError'?error.message:'Admin contract forwarding failed');try{await route.abort();}catch{/* Page closed. */}}
  });
  await use();await page.unrouteAll({behavior:'wait'});expect(errors).toEqual([]);
 },{auto:true}],
});
