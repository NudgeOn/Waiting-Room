// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import assert from 'node:assert/strict';
import Ajv2020 from 'ajv/dist/2020.js';
const api=JSON.parse(fs.readFileSync(new URL('../../api/openapi/public-v1.yaml',import.meta.url),'utf8'));
const validators=new Map();
function validate(key,schema,body){if(!validators.has(key))validators.set(key,new Ajv2020({strict:false,validateFormats:false}).compile({...schema,components:api.components}));assert.equal(validators.get(key)(body),true,'public response schema mismatch '+key);}
export function publicSchema(name,body){validate(name,api.components.schemas[name],body);}
export function publicResponse(method,url,status,headers,body){const path=new URL(url,'https://public.test').pathname;const match=Object.entries(api.paths).find(([template])=>new RegExp('^'+template.replace(/\{[^}]+\}/g,'[^/]+')+'$').test(path));assert.ok(match,'documented public path');const op=match[1][method.toLowerCase()];assert.ok(op,'documented public operation');const response=op.responses[String(status)];assert.ok(response,'public status '+status);assert.equal(headers['cache-control'],'no-store');if(status===204)return op.operationId;const type=(headers['content-type']??'').split(';')[0];assert.ok(response.content?.[type],'public response media type');validate(op.operationId+':'+status,response.content[type].schema,body);return op.operationId;}
