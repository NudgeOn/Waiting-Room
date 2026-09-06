// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {auditDisplay} from '../src/audit.js';
test('audit uses the public at field and does not label accepted operations as rejected',()=>{
 const value=auditDisplay({action:'runtime.operate',result:'accepted',at:'2026-09-06T00:00:00Z'});
 assert.equal(value.action,'대기열 운영');assert.equal(value.result,'적용 명령 저장');assert.equal(value.dateTime,'2026-09-06T00:00:00.000Z');assert.ok(!value.time.includes('Invalid'));
});
test('system recovery labels reflect held/resumed; unknown labels and invalid time fail safely',()=>{
 assert.equal(auditDisplay({action:'runtime.recovery',result:'held'}).result,'안전 대기 시작');assert.equal(auditDisplay({action:'runtime.recovery',result:'resumed'}).result,'검증 후 복구');
 const value=auditDisplay({action:'unknown-sensitive-value',result:'unknown-sensitive-value',at:'bad'});assert.equal(value.dateTime,undefined);assert.ok(!JSON.stringify(value).includes('unknown-sensitive-value'));
});
test('authentication audit distinguishes second factor from completed login',()=>{
 assert.equal(auditDisplay({action:'auth.login',result:'challenge_required'}).result,'TOTP 확인 필요');
 assert.equal(auditDisplay({action:'auth.login',result:'authenticated'}).result,'인증 완료');
 assert.equal(auditDisplay({action:'auth.lockout',result:'locked'}).action,'인증 잠금');
 assert.equal(auditDisplay({action:'auth.logout',result:'logged_out'}).result,'세션 종료');
});
