// SPDX-License-Identifier: Apache-2.0
export const USER_PATTERN='(?:[A-Za-z0-9_]|-){1,128}';
export const RECOVERY_PATTERN='(?:[A-Za-z0-9_]|-){43}';
export class APIError extends Error {
  constructor(status) {super(({401:'인증 정보가 올바르지 않거나 만료되었습니다.',403:'요청이 거부되었습니다. 로그인한 탭에서 다시 시도하세요.',429:'요청이 많습니다. 60초 후 다시 시도하세요.',503:'서버가 잠시 응답할 수 없습니다. 잠시 후 다시 시도하세요.'})[status]??'요청을 완료하지 못했습니다. 입력을 확인하세요.');this.status=status;}
}
export async function api(path,{body,proof,installToken,csrf,method='POST'}={}) {
  const headers={'X-WR-Auth':'1'};
  if(body!==undefined)headers['Content-Type']='application/json';
  if(proof)headers.Authorization='Bearer '+proof;
  if(installToken)headers['X-Bootstrap-Token']=installToken;
  if(csrf)headers['X-CSRF-Token']=csrf;
  let res;try{res=await fetch('/api/admin/v1'+path,{method,headers,credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.timeout(30000),body:body===undefined?undefined:JSON.stringify(body)});}catch{throw new APIError(503);}
  if(!res.ok)throw new APIError(res.status);
  return res.json();
}
// Only the session-bound anti-CSRF proof is persisted per tab, never credentials.
const csrfKey='wr.admin.csrf.v1';
export function readCSRF(){try{const v=sessionStorage.getItem(csrfKey);return /^[A-Za-z0-9_-]{43}$/.test(v??'')?v:'';}catch{return '';}}
export function saveCSRF(value){try{if(value)sessionStorage.setItem(csrfKey,value);else sessionStorage.removeItem(csrfKey);}catch{/* Private browsing may disallow storage; memory state still works. */}}

export function provisioningURI(secret,account){
  if(!/^[A-Z2-7]{32}$/.test(secret))throw new Error('invalid manual key');
  const query=new URLSearchParams({secret,issuer:'Waiting Room',algorithm:'SHA1',digits:'6',period:'30'});
  return 'otpauth://totp/'+encodeURIComponent('Waiting Room:'+account)+'?'+query;
}
