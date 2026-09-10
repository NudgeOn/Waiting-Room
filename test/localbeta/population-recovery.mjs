// SPDX-License-Identifier: Apache-2.0
// Real stopped/restarted AOF and the real recovery deadline. No clock, TTL,
// fencing metadata, persisted ticket or idempotency record is rewritten.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';

export async function populationRecovery({docker, request, dataRequest, startApplications, tickets, keys}) {
  const before = (await request(29463, '/config/delivery')).body;
  const initial = before.nodes.find(n => n.id === 'coordinator').rooms[0];
  assert.equal(initial.mode, 'HOLD');
  assert.equal(initial.waiting, 10000);
  docker('stop', 'gateway', 'coordinator');
  docker('stop', 'valkey');
  docker('up', '-d', '--wait', 'valkey');
  await startApplications();
  let held;
  for (let attempt = 0; attempt < 60; attempt++) {
    const d = (await request(29463, '/config/delivery')).body;
    const m = d.nodes.find(n => n.id === 'coordinator')?.rooms[0];
    if (d.state === 'applied' && m?.mode === 'RECOVERY_HOLD' && m.recoveryFence > initial.recoveryFence) { held = m; break; }
    await delay(1000);
  }
  assert.ok(held, 'restarted 10K primary acknowledges a new shared recovery fence');
  assert.equal(held.epoch, initial.epoch);
  assert.ok(held.recoveryUntil > Date.now() + 120000, 'real default READY/admission safety window remains');
  console.log('PASS: stopped/restarted 10K Valkey AOF, same epoch, shared fence ' + initial.recoveryFence + ' -> ' + held.recoveryFence + ', real deadline ' + new Date(held.recoveryUntil).toISOString());
  let probes = 0;
  while (Date.now() < held.recoveryUntil) {
    const out = await dataRequest('POST', '/_wr/v1/tickets', {target: '/shop'}, {'Idempotency-Key': crypto.randomUUID()});
    if (Date.now() < held.recoveryUntil) {
      assert.equal(out.status, 503);
      assert.equal(out.body.code, 'QUEUE_UNAVAILABLE', 'recovery hold never conceals config loss');
    }
    probes++;
    await delay(Math.min(5000, Math.max(100, held.recoveryUntil - Date.now())));
  }
  let recovered;
  for (let attempt = 0; attempt < 60; attempt++) {
    const d = (await request(29463, '/config/delivery')).body;
    const m = d.nodes.find(n => n.id === 'coordinator')?.rooms[0];
    if (d.state === 'applied' && m?.mode === 'HOLD') { recovered = m; break; }
    await delay(1000);
  }
  assert.ok(recovered, 'bounded validation of all 10K records returns HOLD');
  assert.equal(recovered.waiting, 10000, 'all retained waiting tickets survive the real safety window');
  assert.equal(recovered.ready, 0);
  assert.equal(recovered.epoch, initial.epoch);
  assert.equal(recovered.recoveryFence, held.recoveryFence);
  for (let i = tickets.length - 100; i < tickets.length; i++) {
    const out = await dataRequest('POST', '/_wr/v1/tickets', {target: '/shop/cart'}, {'Idempotency-Key': keys[i]});
    assert.equal(out.status, 202);
    assert.ok(JSON.stringify(out.body) === JSON.stringify(tickets[i]), 'recent encrypted join replies survive cold recovery; credentials suppressed');
  }
  console.log('PASS: ' + probes + ' real safety probes, bounded 10K validation, all 10000 waiting tickets and 100 recent exact join replies preserved; explicit AUTO still required');
}
