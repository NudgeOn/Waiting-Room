// SPDX-License-Identifier: Apache-2.0
import {controlAPI} from './control-api.js';

// Freeze exact UTF-8 bytes and revision at the moment the user requests a
// sensitive action, not at confirmation time or after background polling.
export function prepareAction({path,method,target,action,body,etag,label}) {
  return Object.freeze({path,method,target,action,rawBody:body===undefined?'':JSON.stringify(body),etag:etag??'',label,key:crypto.randomUUID()});
}
export async function actionDigest(request) {
  const bytes=new TextEncoder().encode(`wr-action/v1\n${request.method}\n${request.target}\n${request.etag}\n${request.rawBody}`);
  const hash=await crypto.subtle.digest('SHA-256',bytes);
  return Array.from(new Uint8Array(hash),x=>x.toString(16).padStart(2,'0')).join('');
}
export async function reauthenticate(request,csrf,password,totp) {
  const body={password,action:request.action,targetId:request.target,requestDigest:await actionDigest(request)};
  if(totp)body.totp=totp;
  const out=await controlAPI('/auth/reauth',{method:'POST',csrf,body});
  return out.data.reauthToken;
}
export function executeAction(request,csrf,proof) {
  return controlAPI(request.path,{method:request.method,rawBody:request.rawBody,csrf,reauth:proof,etag:request.etag,key:request.key});
}
