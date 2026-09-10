// SPDX-License-Identifier: Apache-2.0
// Stop only services selected through this fixture's own Compose wrapper.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {publicSchema} from './public-contracts.mjs';

export async function publicFaultChecks({docker, dataRequest, api, until, admissionToken, ticket, authTicket, room}) {
  const admitted = {'X-Waiting-Room-Admission': admissionToken};
  let stopped = false;
  try {
    docker('stop', 'demo-origin'); stopped = true;
    for (const method of ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE']) {
      const out = await dataRequest(method, '/shop/cart', undefined, admitted);
      assert.equal(out.status, 503, 'unreachable origin ' + method);
      assert.equal(out.headers['cache-control'], 'no-store');
      if (method !== 'HEAD') {
        publicSchema('Problem', out.body);
        assert.equal(out.body.code, 'QUEUE_UNAVAILABLE');
      }
    }
    assert.equal((await dataRequest('POST', '/shop/cart')).status, 429, 'origin loss never opens protected routes');
  } finally { if (stopped) docker('start', 'demo-origin'); }
  await until(async () => (await dataRequest('GET', '/shop/cart', undefined, admitted)).status === 200);

  stopped = false;
  try {
    docker('stop', 'coordinator'); stopped = true;
    assert.equal((await dataRequest('GET', '/shop/cart', undefined, admitted)).status, 200, 'valid admission survives Coordinator loss');
    assert.equal((await dataRequest('POST', '/shop/cart')).status, 429, 'Coordinator loss does not bypass admission');
    for (const [method, url, body, headers] of [
      ['POST', '/_wr/v1/tickets', {target: '/shop'}, {'Idempotency-Key': crypto.randomUUID()}],
      ['GET', ticket.statusUrl, undefined, authTicket],
      ['POST', '/_wr/v1/rooms/' + room.publicId + '/admissions', undefined, authTicket],
      ['POST', '/_wr/v1/rooms/' + room.publicId + '/heartbeat', undefined, authTicket],
    ]) {
      const out = await api(method, url, body, headers);
      assert.equal(out.status, 503, 'Coordinator-down public operation');
      assert.equal(out.body.code, 'QUEUE_UNAVAILABLE');
    }
  } finally { if (stopped) docker('start', 'coordinator'); }
  await until(async () => {
    const out = await api('POST', '/_wr/v1/rooms/' + room.publicId + '/admissions', undefined, authTicket);
    if (out.status === 503) return false;
    assert.equal(out.status, 200);
    assert.ok(out.body.admissionToken === admissionToken, 'restart retains exact admission replay; credentials suppressed');
    return true;
  });
  assert.equal((await dataRequest('GET', '/shop/cart', undefined, admitted)).status, 200);
  console.log('PASS: actual origin-down six-method 503 matrix, Coordinator-down four public APIs, valid-admission continuity, safe service restart and byte-identical claim recovery');
}
