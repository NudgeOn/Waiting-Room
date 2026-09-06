// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {prepareAction,actionDigest,executeAction,reauthenticate} from '../src/security-api.js';
test('sensitive request freezes original bytes and matches Go digest framing',async()=>{
 const body={role:'operator',enabled:true};const request=prepareAction({path:'/users/admin2',method:'PATCH',target:'admin2',action:'users.write',body,etag:'"user-4"'});body.role='admin';
 assert.equal(request.rawBody,'{"role":"operator","enabled":true}');assert.ok(Object.isFrozen(request));
 assert.equal(await actionDigest(request),createHash('sha256').update('wr-action/v1\nPATCH\nadmin2\n"user-4"\n{"role":"operator","enabled":true}').digest('hex'));
});
test('reauth and execution share exact body, revision and stable retry key',async()=>{
 const old=globalThis.fetch,calls=[];globalThis.fetch=async(url,options)=>{calls.push({url,...options});return {ok:true,headers:new Headers(),json:async()=>url.endsWith('/reauth')?{reauthToken:'proof'}:{id:'admin2'}};};
 try{const request=prepareAction({path:'/users/admin2',method:'DELETE',target:'admin2',action:'users.write',etag:'"user-4"'});const proof=await reauthenticate(request,'csrf','password','123456');await executeAction(request,'csrf',proof);await executeAction(request,'csrf',proof);
 assert.equal(calls[0].url,'/api/admin/v1/auth/reauth');assert.equal(JSON.parse(calls[0].body).requestDigest,await actionDigest(request));assert.equal(calls[1].body,undefined);assert.equal(calls[1].headers['X-Reauth-Token'],'proof');assert.equal(calls[1].headers['Idempotency-Key'],calls[2].headers['Idempotency-Key']);assert.equal(calls[1].headers['If-Match'],'"user-4"');
 }finally{globalThis.fetch=old;}
});
