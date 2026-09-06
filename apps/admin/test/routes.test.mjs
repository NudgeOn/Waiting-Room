// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {parseRoute} from '../src/routes.js';
test('console routes preserve exact Room identity and tab',()=>{
  assert.deepEqual(parseRoute('/'),{page:'dashboard'});
  assert.deepEqual(parseRoute('/rooms/sale'),{page:'room',roomId:'sale',tab:'operations'});
  for(const tab of ['operations','settings','schedule','verification'])assert.deepEqual(parseRoute('/rooms/sale/'+tab),{page:'room',roomId:'sale',tab});
  assert.equal(parseRoute('/rooms/new').page,'new');
  for(const path of ['/rooms','/settings','/dashboard/runtime','/setup','/auth/login','/auth/session'])assert.notEqual(parseRoute(path).page,'not-found');
});
test('console rejects external, encoded, query and malformed routes',()=>{
  for(const path of ['https://example.test','//example.test','/rooms/%73ale','/rooms/SALE','/rooms/sale/unknown','/rooms/sale/','/rooms/sale?tab=settings','/rooms/../settings','/rooms/'+ 'a'.repeat(65),'/constructor','/__proto__'])assert.equal(parseRoute(path).page,'not-found',path);
});
