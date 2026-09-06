// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {api,provisioningURI,readCSRF,saveCSRF,USER_PATTERN,RECOVERY_PATTERN} from '../src/api.js';
test('HTML patterns compile in UnicodeSets v mode and preserve ID/code bounds',()=>{
  const user=new RegExp('^(?:'+USER_PATTERN+')$','v'),code=new RegExp('^(?:'+RECOVERY_PATTERN+')$','v');
  assert.ok(user.test('test-user_1'));for(const x of ['','a/b','.','a'.repeat(129)])assert.equal(user.test(x),false);
  assert.ok(code.test('-'.repeat(43)));assert.equal(code.test('A'.repeat(42)),false);assert.equal(code.test('/'.repeat(43)),false);
});
test('local QR URI encodes account and exact algorithm without remote service',()=>{
  const uri=new URL(provisioningURI('A'.repeat(32),'a:b /?'));
  assert.equal(uri.protocol,'otpauth:');assert.equal(uri.hostname,'totp');
  assert.equal(decodeURIComponent(uri.pathname),'/Waiting Room:a:b /?');
  assert.equal(uri.searchParams.get('secret'),'A'.repeat(32));assert.equal(uri.searchParams.get('digits'),'6');
  assert.equal(uri.searchParams.get('period'),'30');assert.throws(()=>provisioningURI('bad','a'));
});
test('client keeps credentials in same-origin headers/body and rejects redirects',async()=>{
  const old=globalThis.fetch;let request;
  globalThis.fetch=async(path,options)=>{request={path,...options};return {ok:true,json:async()=>({state:'totp_required'})};};
  try{await api('/auth/login',{body:{username:'fixture',password:'fixture password'},proof:'challenge'});
    assert.equal(request.path,'/api/admin/v1/auth/login');assert.equal(request.credentials,'same-origin');assert.equal(request.redirect,'error');
    assert.equal(request.headers.Authorization,'Bearer challenge');assert.equal(request.cache,'no-store');assert.equal(request.headers['X-WR-Auth'],'1');
  }finally{globalThis.fetch=old;}
});
test('client error does not reflect backend body or network secrets',async()=>{
  const old=globalThis.fetch;
  try{globalThis.fetch=async()=>({ok:false,status:401,text:async()=>'secret'});await assert.rejects(api('/auth/login'),e=>e.status===401&&!e.message.includes('secret'));
    globalThis.fetch=async()=>{throw Error('private password');};await assert.rejects(api('/auth/login'),e=>e.status===503&&!e.message.includes('password'));
  }finally{globalThis.fetch=old;}
});
test('storage is CSRF only and denied storage fails safely',()=>{
  const old=globalThis.sessionStorage;const data=new Map();globalThis.sessionStorage={getItem:k=>data.get(k),setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)};
  try{saveCSRF('A'.repeat(43));assert.equal(readCSRF(),'A'.repeat(43));assert.deepEqual([...data.keys()],['wr.admin.csrf.v1']);saveCSRF('');assert.equal(readCSRF(),'');globalThis.sessionStorage={getItem(){throw Error();},setItem(){throw Error();},removeItem(){throw Error();}};assert.equal(readCSRF(),'');saveCSRF('A'.repeat(43));}finally{globalThis.sessionStorage=old;}
});
