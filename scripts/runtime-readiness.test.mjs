// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {runtimeReady,waitForRuntime} from './runtime-readiness.mjs';
const healthy=()=>['postgres','valkey','control','gateway','demo-origin','coordinator'].map(Service=>({Service,State:'running',Health:'healthy'}));
test('source startup preserves readiness during bounded coordinator recovery',()=>{
  const rows=healthy();rows[5].Health='unhealthy';
  assert.equal(runtimeReady(JSON.stringify(rows),200000),false);
  assert.throws(()=>runtimeReady(JSON.stringify(rows),3700000));
  rows[2].Health='unhealthy';assert.throws(()=>runtimeReady(JSON.stringify(rows),180000));
  rows[2].Health='healthy';rows[5].State='exited';assert.throws(()=>runtimeReady(JSON.stringify(rows),0));
  assert.throws(()=>runtimeReady(JSON.stringify(healthy().slice(0,5)),180000));
  assert.throws(()=>runtimeReady('invalid',0));
  assert.equal(runtimeReady(healthy().map(row=>JSON.stringify(row)).join('\n'),0),true);
});
test('source startup waits for all roles and enforces deadline',async()=>{
  let calls=0;const rows=healthy();rows[5].Health='starting';
  await waitForRuntime(()=>JSON.stringify(++calls<3?rows:healthy()),{interval:1,timeout:1000});
  assert.equal(calls,3);
  await assert.rejects(waitForRuntime(()=>JSON.stringify(rows),{interval:1,timeout:5}));
});
