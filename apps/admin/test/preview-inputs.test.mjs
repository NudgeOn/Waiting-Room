// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {initialPlan, planSteps, validatePlanStep, validatePrice} from '../src/preview-inputs.js';

const plan = () => structuredClone(initialPlan);
const price = () => JSON.parse(fs.readFileSync(new URL('../../../test/installplan/cost-example.json', import.meta.url), 'utf8'));

test('default plan is valid at every input step and exports the guided sequence', () => {
  assert.deepEqual(planSteps, ['규모 선택', '운영 환경', '유량 설정', '관리자 보안', '계획 확인', '비용 비교']);
  for (let step = 0; step < 4; step++) assert.deepEqual(validatePlanStep(initialPlan, step), {});
  assert.deepEqual(validatePlanStep(initialPlan, 4), {});
  assert.deepEqual(validatePlanStep(initialPlan, 5), {});
});

test('profile and region errors are scoped to their own step', () => {
  const draft = {...plan(), profile: 'multi-region', regionId: 'SEOUL'};
  assert.deepEqual(Object.keys(validatePlanStep(draft, 0)), ['profile']);
  assert.deepEqual(Object.keys(validatePlanStep(draft, 1)), ['regionId']);
  for (const regionId of ['', '1seoul', 'a_b', 'a/b', 'a b', '서울', 'a\n', 'a'.repeat(64), null]) {
    assert.ok(validatePlanStep({...plan(), regionId}, 1).regionId, String(regionId));
  }
  for (const regionId of ['a', 'ap-northeast-2', 'a'.repeat(63), 'a-']) {
    assert.deepEqual(validatePlanStep({...plan(), regionId}, 1), {});
  }
});

test('capacity honors each profile and changing to Standard preserves invalid values', () => {
  const draft = {...plan(), profile: 'high-scale-100k', expectedPeakVisitors: 100000,
    limits: {maxActiveAdmissionLeases: 100000, admissionsPerMinute: 60000, admissionTtlSeconds: 3600}};
  assert.deepEqual(validatePlanStep(draft, 2), {});
  const smaller = {...draft, profile: 'standard-10k'};
  const before = structuredClone(smaller);
  assert.deepEqual(Object.keys(validatePlanStep(smaller, 2)), ['expectedPeakVisitors', 'maxActiveAdmissionLeases', 'admissionsPerMinute']);
  assert.deepEqual(smaller, before);
  for (const [profile, cap, rate] of [['standard-10k', 10000, 6000], ['high-scale-100k', 100000, 60000]]) {
    assert.deepEqual(validatePlanStep({...plan(), profile, expectedPeakVisitors: 1,
      limits: {maxActiveAdmissionLeases: 1, admissionsPerMinute: 1, admissionTtlSeconds: 60}}, 2), {});
    const over = {...plan(), profile, expectedPeakVisitors: cap + 1,
      limits: {maxActiveAdmissionLeases: cap + 1, admissionsPerMinute: rate + 1, admissionTtlSeconds: 3601}};
    assert.equal(Object.keys(validatePlanStep(over, 2)).length, 4);
  }
});

test('capacity rejects missing, fractional, non-finite and string integers', () => {
  for (const value of ['', '1', 0, -1, 1.5, NaN, Infinity, null, undefined]) {
    assert.ok(validatePlanStep({...plan(), expectedPeakVisitors: value}, 2).expectedPeakVisitors, String(value));
  }
  for (const value of [59, 3601, 60.1, '60', null]) {
    assert.ok(validatePlanStep({...plan(), limits: {...plan().limits, admissionTtlSeconds: value}}, 2).admissionTtlSeconds);
  }
  assert.equal(Object.keys(validatePlanStep(null, 2)).length, 4);
});

test('TOTP accepts an explicit configurable OFF and rejects missing or forced OFF', () => {
  for (const totp of [{mode: 'configurable', enabled: false}, {mode: 'configurable', enabled: true}, {mode: 'forced_on', enabled: true}]) {
    assert.deepEqual(validatePlanStep({...plan(), totp}, 3), {});
  }
  assert.deepEqual(Object.keys(validatePlanStep({...plan(), totp: {mode: 'forced_on', enabled: false}}, 3)), ['totpEnabled']);
  assert.deepEqual(Object.keys(validatePlanStep({...plan(), totp: {mode: 'unknown', enabled: 'true'}}, 3)), ['totpMode', 'totpEnabled']);
  assert.deepEqual(Object.keys(validatePlanStep({...plan(), totp: undefined}, 3)), ['totpMode', 'totpEnabled']);
});

test('cost fixture validates without changing inputs and region remains the caller responsibility', () => {
  const input = price();
  const before = structuredClone(input);
  assert.deepEqual(validatePrice(input), {});
  assert.deepEqual(input, before);
  delete input.regionId;
  assert.deepEqual(validatePrice(input), {});
  assert.ok(validatePrice({...input, schemaVersion: 2}).schemaVersion);
});

test('price strings preserve exact decimal syntax and reject incompatible values', () => {
  for (const value of ['0', '1', '0.000001', '999999999.999999']) {
    assert.deepEqual(validatePrice({...price(), standardHostHourly: value}), {});
  }
  for (const value of ['', ' 1', '1 ', '1\n', '01', '.1', '1.', '-1', '+1', '1e2', '1,000', '1000000000', '0.1234567', 1, null]) {
    for (const key of ['standardHostHourly', 'highWorkerHourly', 'volumeGiBMonthly']) {
      assert.deepEqual(Object.keys(validatePrice({...price(), [key]: value})), [key], `${key}: ${String(value)}`);
    }
  }
});

test('pricing date validation covers real calendar and backend year bounds', () => {
  for (const asOf of ['2000-01-01', '2000-02-29', '2024-02-29', '2400-02-29', '9999-12-31']) {
    assert.deepEqual(validatePrice({...price(), asOf}), {}, asOf);
  }
  for (const asOf of ['1999-12-31', '10000-01-01', '2023-02-29', '2100-02-29', '2026-04-31', '2026-00-01', '2026-13-01', '2026-01-00', '2026-9-06', '2026-09-06\n', '', null]) {
    assert.deepEqual(Object.keys(validatePrice({...price(), asOf})), ['asOf'], String(asOf));
  }
});

test('pricing identifiers and integer bounds match backend constraints', () => {
  for (const [key, values] of [['provider', ['', 'AWS', '1cloud', 'a_b', 'a\n', 'a'.repeat(64)]], ['currency', ['', 'usd', 'US', 'USDD', 'USD\n', '₩']]]) {
    for (const value of values) assert.deepEqual(Object.keys(validatePrice({...price(), [key]: value})), [key]);
  }
  for (const [key, min, max] of [['fractionDigits', 0, 4], ['monthlyHours', 1, 744], ['highWorkerVolumeGiB', 1, 65536], ['expectedEgressGiB', 0, 1000000000]]) {
    for (const value of [min, max]) assert.deepEqual(validatePrice({...price(), [key]: value}), {});
    for (const value of [min - 1, max + 1, min + 0.5, String(min), '', null, NaN, Infinity]) {
      assert.deepEqual(Object.keys(validatePrice({...price(), [key]: value})), [key], `${key}: ${String(value)}`);
    }
  }
});
