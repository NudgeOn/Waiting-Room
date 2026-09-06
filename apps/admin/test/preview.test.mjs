// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {previewRequest} from '../src/preview-api.js';
test('preview uses explicit same-origin endpoint without cookies',async()=>{
  const old=globalThis.fetch;let options;
  globalThis.fetch=async(url,o)=>{assert.equal(url,'/preview/plan');options=o;return {ok:true,json:async()=>({executable:false})};};
  try{assert.deepEqual(await previewRequest('plan',{profile:'standard-10k'}),{executable:false});assert.equal(options.credentials,'omit');assert.equal(options.cache,'no-store');assert.equal(options.redirect,'error');assert.equal(options.headers['X-WR-Preview'],'1');}finally{globalThis.fetch=old;}
});
test('preview never reflects backend or network error values',async()=>{
  const old=globalThis.fetch;
  try{globalThis.fetch=async()=>({ok:false,status:422,text:async()=>'DO-NOT-ECHO'});await assert.rejects(previewRequest('estimate',{}),e=>!e.message.includes('DO-NOT-ECHO'));globalThis.fetch=async()=>{throw Error('DO-NOT-ECHO');};await assert.rejects(previewRequest('plan',{}),e=>!e.message.includes('DO-NOT-ECHO'));await assert.rejects(previewRequest('apply',{}));}finally{globalThis.fetch=old;}
});
