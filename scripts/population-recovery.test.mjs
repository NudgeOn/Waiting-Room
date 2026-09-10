// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {verifyColdJoinReplay} from '../test/localbeta/population-recovery.mjs';

const ticket = {state: 'queued', expiresAt: '2026-09-10T09:12:30.500Z', ticketToken: 'suppressed-fixture-credential'};
const capacity = date => ({status: 503, body: {code: 'QUEUE_CAPACITY_EXCEEDED'}, headers: {date}});

test('cold recovery distinguishes retained replay from expired replay at full capacity', () => {
  assert.equal(verifyColdJoinReplay({status: 202, body: {...ticket}}, ticket), 'exact');
  assert.equal(verifyColdJoinReplay(capacity('Thu, 10 Sep 2026 09:12:31 GMT'), ticket), 'expired');
});

test('cold recovery never accepts other failures or capacity before a proven replay deadline', () => {
  for (const out of [
    capacity('Thu, 10 Sep 2026 09:12:29 GMT'),
    capacity('Thu, 10 Sep 2026 09:12:30 GMT'),
    capacity('invalid'),
    {status: 503, body: {code: 'QUEUE_UNAVAILABLE'}},
    {status: 503, body: {code: 'CONFIG_UNAVAILABLE'}},
    {status: 429, body: {code: 'API_RATE_LIMITED'}},
    {status: 202, body: {...ticket, ticketToken: 'different-fixture-credential'}},
  ]) assert.throws(() => verifyColdJoinReplay(out, ticket), error => error.code === 'ERR_ASSERTION' && !error.message.includes('fixture-credential'));
});
