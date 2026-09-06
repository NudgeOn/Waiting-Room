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
