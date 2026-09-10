// SPDX-License-Identifier: Apache-2.0
// Stop only services selected through this fixture's own Compose wrapper.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {publicSchema} from './public-contracts.mjs';

// Pause only the fresh fixture before any admission or expiry maintenance is
// due. This isolates loss of the required public guard from an uncertain queue
// write, whose stronger fence is tested separately by the TCP-loss regressions.
export async function publicValkeyPauseChecks({docker, dataRequest, api, request, until, allowedStatus, ticket, authTicket, room, joinKey, joinBody, joinRaw}) {
  const state = async () => {
    const out = await request(29473, '/config/delivery');
    assert.equal(out.status, 200);
    const node = out.body.nodes.find(n => n.id === 'coordinator');
    return {...node?.rooms[0], observedAt: node?.observedAt, fresh: node?.fresh};
  };
  let before;
  await until(async () => {
    before = await state();
    return before.fresh && before.mode === 'HOLD' && before.waiting === 1;
  });
  assert.ok(Number.isInteger(before.epoch) && before.epoch > 0);
  assert.ok(Number.isInteger(before.recoveryFence) && before.recoveryFence > 0);
  let paused = false;
  try {
    docker('pause', 'valkey'); paused = true;
    const responses = await Promise.all([
      api('POST', '/_wr/v1/tickets', {target: '/shop/pause'}, {'Idempotency-Key': crypto.randomUUID()}),
      api('GET', ticket.statusUrl, undefined, authTicket),
      api('POST', '/_wr/v1/rooms/' + room.publicId + '/admissions', undefined, authTicket),
      api('POST', '/_wr/v1/rooms/' + room.publicId + '/heartbeat', undefined, authTicket),
    ]);
    for (const out of responses) {
      assert.equal(out.status, 503, 'paused Valkey must close every public queue API');
      assert.equal(out.body.code, 'QUEUE_UNAVAILABLE');
      assert.equal(out.headers['cache-control'], 'no-store');
    }
    assert.equal((await dataRequest('POST', '/shop/cart')).status, 429, 'storage loss never bypasses protected origin');
  } finally { if (paused) docker('unpause', 'valkey'); }
  const resumed = Date.now();
  // A reconnect can return 503 briefly; it must recover inside the existing
  // fixture's bounded readiness window, with no service restart or new epoch.
  await until(async () => {
    const out = await api('GET', ticket.statusUrl, undefined, authTicket);
    if (out.status === 503) { assert.equal(out.body.code, 'QUEUE_UNAVAILABLE'); return false; }
    assert.ok([202, 429].includes(out.status));
    return true;
  });
  const visible = await allowedStatus(ticket, authTicket);
  assert.equal(visible.status, 202);
  assert.equal(visible.body.state, 'queued');
  const replay = await api('POST', '/_wr/v1/tickets', joinBody, {'Idempotency-Key': joinKey});
  assert.equal(replay.status, 202);
  assert.ok(replay.raw === joinRaw, 'recovered join is byte-identical; credentials suppressed');
  const checkpoint = await state();
  let after;
  await until(async () => {
    after = await state();
    return after.fresh && Date.parse(after.observedAt) > Date.parse(checkpoint.observedAt);
  });
  assert.equal(after.mode, 'HOLD');
  assert.equal(after.epoch, before.epoch);
  assert.equal(after.recoveryFence, before.recoveryFence);
  assert.equal(after.waiting, 1, 'failed pre-write join did not create a visitor');
  console.log('PASS: actual paused-Valkey four-API 503 guard, protected origin closed, same queued ticket and exact join replay recovered without restart/reset; fence unchanged; recovery including scheduled poll=' + (Date.now() - resumed) + 'ms');
}

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
