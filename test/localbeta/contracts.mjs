// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import assert from 'node:assert/strict';
import Ajv2020 from 'ajv/dist/2020.js';
const api=JSON.parse(fs.readFileSync(new URL('../../api/openapi/admin-v1.yaml',import.meta.url),'utf8'));
const validators=new Map();
export function adminSchema(name,value){
 if(!validators.has(name))validators.set(name,new Ajv2020({strict:false,validateFormats:false}).compile({...api.components.schemas[name],components:api.components}));
 assert.equal(validators.get(name)(value),true,'live Admin schema mismatch: '+name); // No response or secret dump.
}

// Validate the observed operation/status/media type rather than an independently
// selected schema. Keep assertion diagnostics free of credentials and response data.
export function adminResponse(method,url,status,headers,body){
 const pathname=new URL(url,'https://admin.test').pathname.replace(/^\/api\/admin\/v1/,'');
 const match=Object.entries(api.paths).find(([template])=>new RegExp('^'+template.replace(/\{[^}]+\}/g,'[^/]+')+'$').test(pathname));
 assert.ok(match,'documented Admin endpoint: '+method+' '+pathname);
 const op=match[1][method.toLowerCase()];assert.ok(op,'documented Admin method');
 assert.notEqual(op['x-implementation-status'],'planned','planned endpoint must not be served');
 const resolve=value=>value?.$ref?value.$ref.slice(2).split('/').reduce((node,key)=>node[key],api):value;
 const response=resolve(op.responses[String(status)]);assert.ok(response,op.operationId+' status '+status);
 const type=(headers['content-type']??'').split(';')[0];const schema=response.content?.[type]?.schema;
 assert.ok(schema,op.operationId+' response media type '+type);
 const key=op.operationId+':'+status+':'+type;
 if(!validators.has(key))validators.set(key,new Ajv2020({strict:false,validateFormats:false}).compile({...schema,components:api.components}));
 assert.equal(validators.get(key)(body),true,'live Admin response mismatch: '+key);
 assert.equal(headers['cache-control'],'no-store');
 for(const [name,header]of Object.entries(response.headers??{})){
  const value=headers[name.toLowerCase()];if(header.required)assert.ok(value,'required response header '+name);
  if(value!==undefined&&header.schema?.const)assert.equal(value,header.schema.const,'response header '+name);
 }
 return op.operationId;
}
