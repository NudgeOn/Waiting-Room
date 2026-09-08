// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {activeRun,mergeRun} from '../src/traffic-lab.js';
import {parseRoute} from '../src/routes.js';
test('Traffic Lab navigation and active states keep interrupted runs distinct',()=>{
 assert.deepEqual(parseRoute('/traffic-lab'),{page:'traffic-lab'});
 for(const state of ['queued','running','cancelling'])assert.equal(activeRun({state}),true);
 for(const state of ['passed','failed','cancelled','interrupted'])assert.equal(activeRun({state}),false);
 assert.deepEqual(mergeRun([{id:'a',state:'queued',createdAt:'2026-01-01'}],{id:'a',state:'running',createdAt:'2026-01-01'}),[{id:'a',state:'running',createdAt:'2026-01-01'}]);
});
