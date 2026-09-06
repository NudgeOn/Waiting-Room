// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {parsePRD,validateGraph,validateDelivery,validateEvidence,isDeliveryEvidence,verifyEvidenceFiles,validateSupportMatrix,validateBenchmarkProfiles,finalGate} from './prd.mjs';

const record=(id,deps=[])=>({data:{id,depends_on:deps,delivery_decision:'NO-GO'},body:''});
test('front matter rejects duplicate keys and absent metadata',()=>{
  assert.throws(()=>parsePRD('# missing'));
  assert.throws(()=>parsePRD('---\nid: A\nid: B\ndelivery_decision: GO\n---\n'));
});
test('dependency validator detects missing owners, duplicate IDs and cycles',()=>{
  assert.deepEqual(validateGraph([record('A'),record('B',['A'])]),[]);
  assert.ok(validateGraph([record('A',['B']),record('B',['A'])]).some(e=>e.includes('cycle')));
  assert.ok(validateGraph([record('A',['missing'])]).length);
  assert.ok(validateGraph([record('A'),record('A')]).length);
});
test('unchecked work and missing evidence cannot become GO',()=>{
  const r=record('A');r.data.delivery_decision='GO';r.body='- [ ] work\n| UT-01 | NOT RUN |';
  const errors=validateDelivery(r,new Map());
  for(const text of ['unchecked','incomplete','reviewer','evidence']) assert.ok(errors.some(e=>e.includes(text)));
});
test('100K child failure blocks final release even if other children report GO',()=>{
  const records=Array.from({length:8},(_,i)=>record('SUB-PRD-'+String(i+1).padStart(2,'0')));
  for(const r of records)r.data={...r.data,delivery_decision:'GO',evidence_status:'PASS',reviewed_by:'test',reviewed_at:'2026-09-05',evidence_file:'test.json'};
  assert.deepEqual(finalGate(records),[]);
  records[6].data.delivery_decision='NO-GO';
  assert.ok(finalGate(records).some(e=>e.includes('SUB-PRD-07')));
});
test('declared dependency failure blocks delivery',()=>{
  const a=record('A');const b=record('B',['A']);b.data.delivery_decision='GO';
  assert.ok(validateDelivery(b,new Map([['A',a],['B',b]])).some(e=>e.includes('Dependency not GO')));
});
const schema=JSON.parse(fs.readFileSync(new URL('../test/fixtures/evidence.schema.json',import.meta.url)));
const evidence=()=>({schemaVersion:1,scope:'unit',runId:'r',sourceDigest:'a'.repeat(64),status:'PASS',checks:[{id:'t',status:'PASS',command:'go test',artifact:'test.log',sha256:'b'.repeat(64)}]});
test('local and M0 success cannot stand in for delivery evidence',()=>{
  assert.equal(isDeliveryEvidence({...evidence(),scope:'local'}),false);
  assert.equal(isDeliveryEvidence({...evidence(),scope:'m0'}),false);
  assert.equal(isDeliveryEvidence({...evidence(),status:'FAIL'}),false);
  assert.equal(isDeliveryEvidence(evidence()),true);
});
test('evidence rejects missing identity, skipped PASS and traversal',()=>{
  assert.deepEqual(validateEvidence(evidence(),schema),[]);
  const missing=evidence();delete missing.sourceDigest;assert.ok(validateEvidence(missing,schema).length);
  const skipped=evidence();skipped.checks[0].status='SKIP';assert.ok(validateEvidence(skipped,schema).length);
  const traversal=evidence();traversal.checks[0].artifact='../secret';assert.ok(validateEvidence(traversal,schema).length);
});
test('artifact digest mismatch and missing logs invalidate evidence',()=>{
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'wr-evidence-test-'));
  try {
    assert.ok(verifyEvidenceFiles(evidence(),root).some(e=>e.includes('Missing')));
    fs.writeFileSync(path.join(root,'test.log'),'tampered');
    assert.ok(verifyEvidenceFiles(evidence(),root).some(e=>e.includes('mismatch')));
  } finally {fs.rmSync(root,{recursive:true});}
});
test('support matrix rejects wildcard versions and false qualification',()=>{
  const base={schemaVersion:1,valkeyTopology:'unsharded-single-primary',status:'candidate-not-production-qualified',targets:{postgresql:'17.11',valkey:'8.1.6',kubernetes:'1.35.6',docker:'29.0.0',compose:'2.39.4',helm:'3.19.0'}};
  assert.deepEqual(validateSupportMatrix(base),[]);
  assert.ok(validateSupportMatrix({...base,targets:{...base.targets,valkey:'latest'}}).length);
  assert.ok(validateSupportMatrix({...base,status:'qualified'}).length);
});

test('benchmark targets respect default retention, skew and rolling-rate budgets',()=>{
  const profiles=JSON.parse(fs.readFileSync(new URL('../docs/benchmarks/profiles.yaml',import.meta.url)));
  assert.deepEqual(validateBenchmarkProfiles(profiles),[]);
  const excessive=structuredClone(profiles);excessive.profiles[1].joinPerSecond=1000;excessive.profiles[1].claimPerSecond=1000;
  assert.ok(validateBenchmarkProfiles(excessive).some(e=>e.includes('idempotency capacity')));
  const longTTL=structuredClone(profiles);longTTL.profiles[1].admissionTTLSeconds=900;
  assert.ok(validateBenchmarkProfiles(longTTL).some(e=>e.includes('lease capacity')));
  const override=structuredClone(profiles);override.profiles[1].idempotencyTTLSeconds=120;
  assert.ok(validateBenchmarkProfiles(override).some(e=>e.includes('default idempotency')));
});
