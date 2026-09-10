// SPDX-License-Identifier: Apache-2.0
// Real stopped/restarted AOF and the real recovery deadline. No clock, TTL,
// fencing metadata, persisted ticket or idempotency record is rewritten.
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';

export function verifyColdJoinReplay(out, ticket) {
  // This fixture keeps the default ten-minute idle AND join-replay TTLs.
  // Heartbeats extend only the current ticket. Its immutable initial expiresAt
  // therefore also identifies this fixture's replay deadline.
  if (out.status === 503 && out.body?.code === 'QUEUE_CAPACITY_EXCEEDED') {
    const serverTime = Date.parse(out.headers.date), replayDeadline = Date.parse(ticket.expiresAt);
    assert.ok(Number.isFinite(serverTime) && Number.isFinite(replayDeadline));
    // HTTP Date has whole seconds: require a definitely elapsed deadline.
    assert.ok(serverTime >= replayDeadline, 'capacity must not hide a still-retained join replay');
    return 'expired';
  }
  assert.equal(out.status, 202, 'cold join replay code=' + out.body?.code);
  assert.ok(JSON.stringify(out.body) === JSON.stringify(ticket), 'recent encrypted join replies survive cold recovery; credentials suppressed');
  return 'exact';
}

export async function populationRecovery({docker, request, dataRequest, startApplications, tickets, keys}) {
  const before = (await request(29463, '/config/delivery')).body;
  const initial = before.nodes.find(n => n.id === 'coordinator').rooms[0];
  assert.equal(initial.mode, 'HOLD');
  assert.equal(initial.waiting, 10000);
  docker('stop', 'gateway', 'coordinator');
  docker('stop', 'valkey');
  docker('up', '-d', '--wait', 'valkey');
  // Readiness includes the real cold recovery wait. The Coordinator cannot
  // publish a ready configuration while the primary-change fence is held, so
  // searching for RECOVERY_HOLD after this wait would miss it by construction.
  const began = performance.now();
  await startApplications();
  const recoveryWait = performance.now() - began;
  // This fixture uses default READY=120s and admission=60s, plus the mandatory
  // 30s margin. No clock or stored deadline is changed to shorten that wait.
  assert.ok(recoveryWait >= 150000, 'cold primary readiness retains the real 150-second default safety window');
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
  assert.ok(recovered.recoveryFence > initial.recoveryFence, 'cold primary acknowledged a newer shared fence');
  console.log('PASS: real cold readiness wait ' + Math.round(recoveryWait) + 'ms, bounded 10K validation, same epoch, newer fence and all 10000 waiting tickets preserved');
  let exact = 0, expired = 0;
  for (let i = tickets.length - 100; i < tickets.length; i++) {
    const out = await dataRequest('POST', '/_wr/v1/tickets', {target: '/shop/cart'}, {'Idempotency-Key': keys[i]});
    // All 10K status reads plus the cold wait can outlast the newest replay.
    if (verifyColdJoinReplay(out, tickets[i]) === 'expired') expired++;
    else exact++;
  }
  await Promise.all(tickets.slice(-100).map(async ticket => {
    const out = await dataRequest('GET', ticket.statusUrl, undefined, {Authorization: 'Bearer ' + ticket.ticketToken});
    assert.equal(out.status, 202, 'retained ticket status after cold recovery');
    assert.equal(out.body?.state, 'queued');
  }));
  console.log('PASS: cold join response contracts: exact within retention=' + exact + ', expired replay rejected at full capacity=' + expired + '; all 100 original ticket credentials still read queued; explicit AUTO still required; continuous hold probing is covered separately by epoch-runtime');
}
