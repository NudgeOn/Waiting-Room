// SPDX-License-Identifier: Apache-2.0
// Real isolated runtime only. Every transition waits for both published ACKs.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {publicSchema} from './public-contracts.mjs';
import {publicSurfaceChecks} from './public-surfaces.mjs';

export async function publicModeChecks({request, dataRequest, api, command, until, csrf, password, admissionToken, room, docker, ticket, authTicket, browser, origin}) {
  const methods = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE'];
  let checked = 0;
  let surfaces = 0;
  for (const mode of ['HOLD', 'DRAINING', 'OFF', 'AUTO']) {
    if (mode === 'OFF') {
      const rt = await request(29473, '/rooms/setup_room/runtime');
      const body = {action: 'instant-off'};
      const requestDigest = crypto.createHash('sha256').update('wr-action/v1\nPATCH\nsetup_room\n' + rt.etag + '\n' + JSON.stringify(body)).digest('hex');
      const proof = await request(29473, '/auth/reauth', 'POST', {password, action: 'runtime.instant_off', targetId: 'setup_room', requestDigest}, {'X-CSRF-Token': csrf});
      assert.equal(proof.status, 200, 'Admin action-bound OFF proof');
      const headers = {'X-CSRF-Token': csrf, 'X-Reauth-Token': proof.body.reauthToken, 'If-Match': rt.etag, 'Idempotency-Key': crypto.randomUUID()};
      const changed = await request(29473, '/rooms/setup_room/runtime', 'PATCH', body, headers);
      assert.equal(changed.status, 200);
      const replay = await request(29473, '/rooms/setup_room/runtime', 'PATCH', body, headers);
      assert.deepEqual(replay.body, changed.body);
      assert.equal(replay.headers['idempotency-replayed'], 'true');
      const audit = await request(29473, '/audit-events');
      assert.equal(audit.status, 200);
      assert.equal(audit.body.items.filter(item => item.action === 'runtime.instant_off').length, 1, 'OFF retry creates one audit event');
      await until(async () => (await request(29473, '/config/delivery')).body.state === 'applied');
    } else {
      await command({HOLD: 'hold', DRAINING: 'safe-drain', AUTO: 'auto'}[mode]);
    }
    if(process.env.WR_TEST_PUBLIC_FAULT_MATRIX==='1')surfaces+=await publicSurfaceChecks({mode,docker,dataRequest,api,until,admissionToken,ticket,authTicket,room,browser,origin,withFaults:true});
    for (const credential of ['missing', 'valid', 'invalid']) {
      const headers = credential === 'missing' ? {} : {'X-Waiting-Room-Admission': credential === 'valid' ? admissionToken : 'invalid-admission'};
      for (const method of methods) {
        const body = ['GET', 'HEAD'].includes(method) ? undefined : {customer: 'matrix'};
        const out = await dataRequest(method, '/shop/cart', body, headers);
        const expected = mode === 'OFF' || credential === 'valid' ? 200 : mode === 'DRAINING' ? 503 : 429;
        assert.equal(out.status, expected, `${mode}/${credential}/${method} origin gate`);
        if (expected !== 200) {
          assert.equal(out.headers['cache-control'], 'no-store');
          if (method !== 'HEAD') {
            publicSchema('Problem', out.body);
            assert.equal(out.body.code, mode === 'DRAINING' ? 'QUEUE_DRAINING' : 'WAITING_ROOM_REQUIRED');
          }
        }
        checked++;
      }
    }
    // OFF bypass applies only to customer routes, never reserved API methods
    // or authentication. DRAINING/OFF reject fresh tickets rather than issuing
    // credentials that a stopped queue cannot admit.
    assert.equal((await dataRequest('GET', '/_wr/v1/tickets')).status, 405);
    assert.equal((await api('POST', '/_wr/v1/rooms/' + room.publicId + '/admissions')).status, 401);
    if (['DRAINING', 'OFF'].includes(mode)) {
      const join = await api('POST', '/_wr/v1/tickets', {target: '/shop'}, {'Idempotency-Key': crypto.randomUUID()});
      assert.equal(join.status, 503);
      assert.equal(join.body.code, 'QUEUE_DRAINING');
    }
  }
  console.log('PASS: actual HTTPS mode matrix ' + checked + ' customer requests (HOLD/DRAINING/OFF/AUTO, missing/valid/invalid admission, six methods), reserved API boundaries, Admin OFF exact retry and AUTO restoration');
  if(surfaces)console.log('PASS: expanded mode/failure/browser/app matrix '+surfaces+' actual customer requests, 320px Coordinator outage page, keyboard retry focus and axe 0 violations');
}
