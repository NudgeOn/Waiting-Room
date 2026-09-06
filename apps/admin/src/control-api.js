// SPDX-License-Identifier: Apache-2.0
import {APIError} from './api.js';
export async function controlAPI(path,{body,csrf,etag,key,method='GET'}={}) {
  const headers={};
  if(body!==undefined)headers['Content-Type']='application/json';
  if(csrf)headers['X-CSRF-Token']=csrf;
  if(etag)headers['If-Match']=etag;
  if(key)headers['Idempotency-Key']=key;
  let res;
  try{res=await fetch('/api/admin/v1'+path,{method,headers,credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.timeout(10000),body:body===undefined?undefined:JSON.stringify(body)});}catch{throw new APIError(503);}
  if(!res.ok){const err=new APIError(res.status);if(res.status===412)err.message='다른 운영자가 설정을 변경했습니다. 최신 초안을 불러온 뒤 다시 수정하세요.';if(res.status===409)err.message='경로 또는 재시도 요청이 충돌합니다. 입력과 최신 초안을 확인하세요.';if(res.status===422)err.message='경로·원본 주소·유량·테마 설정이 허용 범위인지 확인하세요.';throw err;}
  if((path==='/config/draft'||path.startsWith('/audit-events'))&&res.headers.get('X-WR-Config-State')!=='draft-only')throw new APIError(503);
  return {data:await res.json(),etag:res.headers.get('ETag'),replay:res.headers.get('Idempotency-Replayed')==='true'};
}
export function publicRoomID(){const alphabet='abcdefghijklmnopqrstuvwxyz234567';return Array.from(crypto.getRandomValues(new Uint8Array(20)),x=>alphabet[x&31]).join('');}
export function newRoom(){return {id:'',publicId:publicRoomID(),name:'',hostname:'',origin:'',healthURL:'',protectPrefixes:['/shop'],excludePrefixes:[],queuePolicy:{kind:'fifo',ticketIdleTtlSeconds:600,ticketMaxTtlSeconds:86400,readyTtlSeconds:120},limits:{maxActiveAdmissionLeases:1000,admissionsPerMinute:600,admissionTtlSeconds:900},theme:{templateId:'calm',title:'잠시만 기다려 주세요',message:'입장 가능한 순서가 되면 안내합니다.',primaryColor:'#105641',locale:'ko',showEstimatedWait:false},active:false};}
export function roomFromForm(form,room){const f=new FormData(form);return {...room,active:f.has('active-present')?f.has('active'):room.active,id:f.get('id'),name:f.get('name'),hostname:f.get('hostname'),origin:f.get('origin'),healthURL:f.get('healthURL'),protectPrefixes:String(f.get('protect')).split('\n').map(x=>x.trim()).filter(Boolean),excludePrefixes:String(f.get('exclude')).split('\n').map(x=>x.trim()).filter(Boolean),limits:{maxActiveAdmissionLeases:Number(f.get('leases')),admissionsPerMinute:Number(f.get('rate')),admissionTtlSeconds:Number(f.get('ttl'))},theme:{...room.theme,title:f.get('title'),message:f.get('message'),primaryColor:f.get('color'),locale:f.get('locale')}};}
