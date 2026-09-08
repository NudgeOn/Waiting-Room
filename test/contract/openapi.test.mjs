// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import Ajv2020 from 'ajv/dist/2020.js';
import {newRoom} from '../../apps/admin/src/control-api.js';
const load=name=>JSON.parse(fs.readFileSync(new URL('../../api/openapi/'+name+'-v1.yaml',import.meta.url)));
const publicAPI=load('public'),adminAPI=load('admin');
test('draft authoring client matches the strict saved ConfigDraft contract',()=>{
  const room={...newRoom(),id:'sale',name:'Sale',hostname:'shop.example.test',origin:'https://origin.example.test',healthURL:'https://origin.example.test/health'};
  const config={schemaVersion:1,revision:0,profile:'standard-10k',regionId:'local',rooms:[room]};
  assert.ok(validateSchema(adminAPI,'ConfigDraft',config));
  const missing=structuredClone(config);delete missing.rooms[0].theme.templateId;
  assert.equal(validateSchema(adminAPI,'ConfigDraft',missing),false);
  assert.equal(validateSchema(adminAPI,'ConfigDraft',{...config,rooms:Array(101).fill(room)}),false);
  const operation=adminAPI.paths['/config/draft'].put;
  for(const name of ['Idempotency-Key','If-Match','X-CSRF-Token'])assert.ok(operation.parameters.some(p=>p.name===name&&p.required));
  assert.ok(operation.responses['412']);assert.ok(operation.responses['503']);
});
test('draft audit contract accepts redacted digests and excludes payloads',()=>{
  const event={id:'1',at:'2026-09-06T00:00:00Z',actorId:'admin',actorRole:'admin',action:'config.write',targetId:'config',result:'saved_draft',requestId:'local-id',revision:1,beforeDigest:'a'.repeat(64),afterDigest:'b'.repeat(64)};
  assert.ok(validateSchema(adminAPI,'Audit',event));
  assert.equal(validateSchema(adminAPI,'Audit',{...event,body:{secret:'no'}}),false);
  assert.equal(validateSchema(adminAPI,'Audit',{...event,beforeDigest:'bad'}),false);
});
function validateSchema(api,name,value){
  const root={...api.components.schemas[name],components:api.components};
  const ajv=new Ajv2020({strict:false,validateFormats:false});
  return ajv.compile(root)(value);
}
test('all local OpenAPI references resolve',()=>{
  for(const api of [publicAPI,adminAPI]){
    function walk(value){
      if(!value||typeof value!=='object')return;
      if(value.$ref){
        assert.ok(value.$ref.startsWith('#/'));
        let item=api;for(const part of value.$ref.slice(2).split('/'))item=item?.[part.replace(/~1/g,'/').replace(/~0/g,'~')];
        assert.notEqual(item,undefined,value.$ref);
      }
      for(const child of Object.values(value))walk(child);
    }walk(api);
  }
});
test('join schema rejects absolute and scheme-relative targets',()=>{
  assert.ok(validateSchema(publicAPI,'JoinRequest',{target:'/shop?x=1'}));
  for(const target of ['https://attacker.test','//attacker.test',''])assert.equal(validateSchema(publicAPI,'JoinRequest',{target}),false);
});
test('Room IDs exclude invalid base32 characters',()=>{
  assert.ok(validateSchema(publicAPI,'RoomID','abcdefghijklmnopqrst'));
  for(const value of ['abc234def567ghi890jk','short','A'.repeat(20)])assert.equal(validateSchema(publicAPI,'RoomID',value),false);
});
test('public status and claim are distinct operations and heartbeat is explicit',()=>{
  const base='/_wr/v1/rooms/{roomPublicId}';
  assert.equal(publicAPI.paths[base+'/status'].get.operationId,'getTicketStatus');
  assert.equal(publicAPI.paths[base+'/admissions'].post.operationId,'claimAdmission');
  assert.equal(publicAPI.paths[base+'/heartbeat'].post.operationId,'heartbeatTicket');
  assert.equal(publicAPI.paths[base+'/status'].post,undefined);
});
test('admin policy and limit schema reject unsupported/unsafe values',()=>{
  const valid={maxActiveAdmissionLeases:1000,admissionsPerMinute:600,admissionTtlSeconds:900};
  assert.ok(validateSchema(adminAPI,'Limits',valid));
  for(const patch of [{maxActiveAdmissionLeases:100001},{admissionsPerMinute:0},{admissionTtlSeconds:1}])
    assert.equal(validateSchema(adminAPI,'Limits',{...valid,...patch}),false);
  assert.equal(validateSchema(adminAPI,'QueuePolicy',{kind:'lottery',ticketIdleTtlSeconds:600,ticketMaxTtlSeconds:86400,readyTtlSeconds:120}),false);
});
test('admin mutations declare authentication and concurrency headers',()=>{
  for(const [path,methods]of Object.entries(adminAPI.paths)){
    for(const [method,op]of Object.entries(methods)){
      if(method==='get'||path.startsWith('/auth/')||path==='/bootstrap')continue;
      if(path.startsWith('/setup/')){assert.deepEqual(op.security,[{bootstrapToken:[]}]);assert.ok(op.parameters.some(p=>p.name==='X-WR-Auth'&&p.required));assert.ok(op.responses['412']);continue;}
      const headers=op.parameters.filter(x=>x.in==='header').map(x=>x.name);
      assert.ok(headers.includes('X-CSRF-Token'),path);
      if(op['x-read-only']===true)assert.equal(path,'/config/route-check');
      else if(op['x-idempotency']==='challenge-bound')assert.ok(path.startsWith('/security/totp/enrollment/'));
      else assert.ok(headers.includes('Idempotency-Key'),path);
      assert.ok(op.security.length,path);
      if(path==='/config'||path.endsWith('/runtime')||path.includes('/events'))
        assert.ok(headers.includes('If-Match')||path==='/config/validate',path);
    }
  }
});

test('route check is a typed read-only POST that cannot reflect target URL',()=>{
 const op=adminAPI.paths['/config/route-check'].post;
 assert.equal(op['x-read-only'],true);assert.deepEqual(op['x-required-roles'],['admin','operator','viewer']);
 assert.ok(validateSchema(adminAPI,'RouteCheckInput',{source:'draft',url:'https://shop.example.test/shop'}));
 for(const value of [{source:'live',url:'https://shop.test'}, {source:'draft',url:''}, {source:'draft'}, {source:'draft',url:'x',extra:true}])assert.equal(validateSchema(adminAPI,'RouteCheckInput',value),false);
 const result={source:'published',scope:'configuration-only',revision:1,generation:1,match:{decision:'protected',reason:'protect_prefix',roomId:'sale'},mode:'HOLD'};
 assert.ok(validateSchema(adminAPI,'RouteCheckResult',result));assert.equal(validateSchema(adminAPI,'RouteCheckResult',{...result,url:'secret'}),false);
});

test('implemented security contracts use lowercase roles, explicit enabled and policy revision',()=>{
 const user={id:'admin',role:'admin',enabled:true,totpEnrolled:true,revision:2,etag:'"user-2"'};
 assert.ok(validateSchema(adminAPI,'User',user));assert.equal(validateSchema(adminAPI,'User',{...user,role:'Admin'}),false);
 assert.ok(validateSchema(adminAPI,'UserUpdate',{role:'viewer',enabled:false}));assert.equal(validateSchema(adminAPI,'UserUpdate',{role:'viewer'}),false);assert.equal(validateSchema(adminAPI,'UserUpdate',{role:'viewer',enabled:null}),false);
 assert.ok(validateSchema(adminAPI,'TOTPPolicy',{mode:'configurable',enabled:false,version:3,enrolled:true}));
 assert.equal(validateSchema(adminAPI,'TOTPUpdate',{}),false);
 assert.ok(adminAPI.paths['/users'].post.responses['201']);
 for(const path of ['/users/{id}','/users/{id}/totp-reset','/security/totp'])for(const [method,op]of Object.entries(adminAPI.paths[path]))if(method!=='get')assert.ok(op.parameters.some(p=>p.name==='If-Match'&&p.required));
 assert.equal(adminAPI.paths['/auth/reauth'].post.responses['200'].headers['Set-Cookie'],undefined);
});
test('runtime publication and authenticated enrollment paths have explicit response contracts',()=>{
 assert.ok(adminAPI.paths['/config/publish'].post.responses['202']);assert.ok(adminAPI.paths['/config/delivery'].get);assert.ok(adminAPI.paths['/events/{id}/resume'].post);
 for(const step of ['start','verify']){const op=adminAPI.paths['/security/totp/enrollment/'+step].post;assert.equal(op['x-idempotency'],'challenge-bound');for(const header of ['Origin','X-CSRF-Token'])assert.ok(op.parameters.some(p=>p.name===header&&p.required));}
 assert.equal(validateSchema(adminAPI,'RuntimeCommand',{action:'new-epoch'}),false);
 assert.equal(validateSchema(adminAPI,'RuntimeCommand',{action:'new-epoch',scope:'installation',generation:1}),true);
 assert.ok(validateSchema(adminAPI,'PublishResult',{generation:2,revision:1,state:'pending'}));
});
test('theme contract supports the built-in template and rejects remote/unknown IDs',()=>{
  const theme={title:'Waiting Room',message:'Please wait',primaryColor:'#12745b',locale:'ko',showEstimatedWait:true};
  assert.ok(validateSchema(adminAPI,'Theme',theme));
  assert.ok(validateSchema(adminAPI,'Theme',{...theme,templateId:'calm'}));
  for(const templateId of ['../calm','remote','https://evil.test/theme'])
    assert.equal(validateSchema(adminAPI,'Theme',{...theme,templateId}),false);
});
test('authentication HTTP contract separates challenges and cookie sessions',()=>{
  const challenge={state:'totp_required',challengeToken:'A'.repeat(43),expiresAt:'2026-09-05T00:05:00Z'};
  assert.ok(validateSchema(adminAPI,'LoginResult',challenge));
  assert.ok(validateSchema(adminAPI,'LoginResult',{...challenge,state:'enrollment_required'}));
  for(const extra of [{csrfToken:'A'.repeat(43)},{sessionToken:'A'.repeat(43)},{state:'authenticated'},{challengeToken:'short'}])assert.equal(validateSchema(adminAPI,'LoginResult',{...challenge,...extra}),false);
  const view={userId:'u',role:'viewer',mfaVerified:true,totpEnrolled:true,capabilityVersion:1,capabilities:[],createdAt:'2026-09-05T00:00:00Z',lastSeenAt:'2026-09-05T00:00:00Z',idleExpiresAt:'2026-09-05T00:30:00Z',absoluteExpiresAt:'2026-09-05T08:00:00Z'};
  assert.ok(validateSchema(adminAPI,'LoginResult',{state:'authenticated',session:view,csrfToken:'A'.repeat(43)}));
  assert.equal(validateSchema(adminAPI,'LoginResult',{state:'authenticated',session:view,csrfToken:'A'.repeat(43),sessionToken:'A'.repeat(43)}),false);
  for(const path of ['/auth/login','/auth/totp/verify','/auth/totp/recover']){
    const op=adminAPI.paths[path].post;
    assert.ok(op.parameters.some(p=>p.name==='Origin'&&p.required));
    assert.ok(op.parameters.some(p=>p.name==='X-WR-Auth'&&p.required&&p.schema.const==='1'));
    for(const status of ['405','413','415','429','503'])assert.ok(op.responses[status]);
    assert.equal(op.responses['429'].headers['Retry-After'].schema.const,'60');
  }
  assert.ok(adminAPI.components.schemas.Problem.properties.code.enum.includes('AUTH_RATE_LIMITED'));
});
test('bootstrap and enrollment HTTP contract restricts policy and recovery material',()=>{
  const credentials={username:'initial_admin',password:'local setup password fixture'};
  assert.ok(validateSchema(adminAPI,'Bootstrap',credentials));
  for(const extra of [{totpEnabled:false},{role:'admin'},{password:'short'},{username:'../admin'}])assert.equal(validateSchema(adminAPI,'Bootstrap',{...credentials,...extra}),false);
  const begin={secret:'A'.repeat(32),expiresAt:'2026-09-05T00:05:00Z'};
  assert.ok(validateSchema(adminAPI,'Enrollment',begin));
  for(const extra of [{csrfToken:'A'.repeat(43)},{secret:'invalid'},{otpauthURI:'https://external.test'}])assert.equal(validateSchema(adminAPI,'Enrollment',{...begin,...extra}),false);
  const session={userId:'u',role:'admin',mfaVerified:true,totpEnrolled:true,capabilityVersion:1,capabilities:[],createdAt:'2026-09-05T00:00:00Z',lastSeenAt:'2026-09-05T00:00:00Z',idleExpiresAt:'2026-09-05T00:30:00Z',absoluteExpiresAt:'2026-09-05T08:00:00Z'};
  const recoveryCodes=Array.from({length:10},(_,i)=>String(i)+'A'.repeat(42));
  const complete={state:'authenticated',session,csrfToken:'A'.repeat(43),recoveryCodes};
  assert.ok(validateSchema(adminAPI,'EnrollmentResult',complete));
  for(const extra of [{recoveryCodes:[]},{recoveryCodes:Array(10).fill(recoveryCodes[0])},{sessionToken:'secret'},{state:'enrollment_required'}])assert.equal(validateSchema(adminAPI,'EnrollmentResult',{...complete,...extra}),false);
  assert.equal(adminAPI.paths['/auth/totp/enroll'].post.responses['200'].headers['Set-Cookie'],undefined);
  for(const path of ['/bootstrap','/auth/totp/enroll','/auth/totp/enroll/verify']){
    const op=adminAPI.paths[path].post;
    for(const header of ['Origin','X-WR-Auth'])assert.ok(op.parameters.some(p=>p.name===header&&p.required));
    for(const status of ['405','413','415','429','503'])assert.ok(op.responses[status]);
  }
});
test('session read/logout contract excludes secrets and declares unavailable responses',()=>{
  const view={userId:'u',role:'viewer',mfaVerified:false,totpEnrolled:false,capabilityVersion:1,
    capabilities:[{action:'dashboard.read',requiresReauthentication:false}],
    createdAt:'2026-09-05T00:00:00Z',lastSeenAt:'2026-09-05T00:00:00Z',
    idleExpiresAt:'2026-09-05T00:30:00Z',absoluteExpiresAt:'2026-09-05T08:00:00Z'};
  assert.ok(validateSchema(adminAPI,'CurrentSession',view));
  for(const extra of [{csrfToken:'secret'},{sessionToken:'secret'},{passwordHash:'secret'},{role:'superuser'},{capabilityVersion:0}])
    assert.equal(validateSchema(adminAPI,'CurrentSession',{...view,...extra}),false);
  const me=adminAPI.paths['/auth/me'].get,logout=adminAPI.paths['/auth/logout'].post;
  assert.equal(me.responses['200'].content['application/json'].schema.$ref,'#/components/schemas/CurrentSession');
  assert.ok(me.responses['503']&&logout.responses['503']);
  assert.ok(logout.security.some(s=>'csrfHeader' in s));
  assert.ok(adminAPI.components.schemas.Problem.properties.code.enum.includes('AUTH_UNAVAILABLE'));
});

test('Traffic Lab accepts fixed presets only and protects mutations with role and retry contracts',()=>{
 for(const code of ['LAB_BUSY','LAB_CAPACITY'])assert.ok(validateSchema(adminAPI,'Problem',{type:'about:blank',title:code,status:code==='LAB_BUSY'?409:429,code,requestId:'local-fixture'}));
 for(const preset of ['quick-20','smoke-1k'])assert.ok(validateSchema(adminAPI,'TrafficLabStart',{preset}));
 for(const value of [{preset:'10k'},{preset:'quick-20',url:'https://customer.test'},{preset:'smoke-1k',roomId:'sale'},{}])assert.equal(validateSchema(adminAPI,'TrafficLabStart',value),false);
 for(const path of ['/lab/runs','/lab/runs/{id}/cancel']){
  const op=adminAPI.paths[path].post;assert.deepEqual(op['x-required-roles'],['admin','operator']);assert.ok(op.responses['202']);
 }
 assert.deepEqual(adminAPI.paths['/lab/runs'].get['x-required-roles'],['admin','operator','viewer']);
});

test('every implemented operation declares current lowercase roles and typed failures',()=>{
 for(const [path,methods] of Object.entries(adminAPI.paths))for(const [method,op]of Object.entries(methods)){
  if(op['x-implementation-status']==='planned')continue;
  assert.ok(Array.isArray(op['x-required-roles']),method+' '+path);
  assert.ok(op['x-required-roles'].every(role=>['admin','operator','viewer'].includes(role)),method+' '+path);
  for(const status of ['400','404','405','503'])assert.ok(op.responses[status],method+' '+path+' '+status);
 }
 assert.deepEqual(adminAPI.paths['/auth/reauth'].post['x-required-roles'],['admin']);
 const logout=adminAPI.paths['/auth/logout'].post;
 assert.equal(logout['x-idempotency'],'session-bound-24h');
 assert.equal(logout.parameters.find(p=>p.name==='Idempotency-Key').required,false);
 assert.ok(logout.responses['409']);
});
