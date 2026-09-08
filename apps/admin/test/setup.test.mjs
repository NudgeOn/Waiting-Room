// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {importSetupInput} from '../src/setup.js';
const input={schemaVersion:1,profile:'standard-10k',regionId:'local',queuePolicy:'fifo',expectedPeakVisitors:100,limits:{maxActiveAdmissionLeases:30,admissionsPerMinute:17,admissionTtlSeconds:120},totp:{mode:'configurable',enabled:false}};
const file=value=>{const text=JSON.stringify(value);return {size:text.length,text:async()=>text};};
test('setup accepts preview reports as editable inputs, retaining explicit false',async()=>{
  assert.deepEqual(await importSetupInput(file({payload:{plan:{input}}})),input);
  assert.deepEqual(await importSetupInput(file({...input,password:'must not import'})),input);
  await assert.rejects(importSetupInput(file({...input,profile:'high-scale-100k'})));
  await assert.rejects(importSetupInput(file({...input,totp:{mode:'forced_on',enabled:null}})));
  await assert.rejects(importSetupInput({size:16385,text:async()=>{throw Error('must not read');}}));
});
