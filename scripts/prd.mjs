// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import YAML from 'yaml';
import Ajv2020 from 'ajv/dist/2020.js';

export function parsePRD(text) {
  const match = text.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  if (!match) throw new Error('Missing PRD front matter');
  const document = YAML.parseDocument(match[1], { uniqueKeys: true });
  if (document.errors.length) throw new Error(document.errors[0].message);
  const data = document.toJS();
  if (!data || typeof data.id !== 'string' || !['GO', 'NO-GO'].includes(data.delivery_decision))
    throw new Error('Invalid PRD ID or delivery decision');
  if (!Array.isArray(data.depends_on ?? [])) throw new Error('depends_on must be an array');
  return { data, body: text.slice(match[0].length) };
}

export function validateGraph(records) {
  const errors = [];
  const byID = new Map();
  for (const r of records) {
    if (byID.has(r.data.id)) errors.push('Duplicate ID: ' + r.data.id);
    byID.set(r.data.id, r);
  }
  const visiting = new Set(), done = new Set();
  function visit(id) {
    if (visiting.has(id)) { errors.push('Dependency cycle: ' + id); return; }
    if (done.has(id)) return;
    visiting.add(id);
    for (const dep of byID.get(id)?.data.depends_on ?? []) {
      if (!byID.has(dep)) errors.push('Unknown dependency: ' + dep);
      else visit(dep);
    }
    visiting.delete(id); done.add(id);
  }
  for (const id of byID.keys()) visit(id);
  return errors;
}

export function validateDelivery(record, byID) {
  if (record.data.delivery_decision !== 'GO') return [];
  const errors = [];
  if (record.data.evidence_status !== 'PASS') errors.push('GO requires PASS evidence');
  if (/^- \[ \]/m.test(record.body)) errors.push('GO contains unchecked requirements');
  if (/\|\s*(NOT RUN|FAIL|SKIP|PARTIAL)\s*\|/.test(record.body)) errors.push('GO contains incomplete test result');
  if (!record.data.reviewed_by || !record.data.reviewed_at) errors.push('GO requires reviewer and timestamp');
  if (!record.data.evidence_file) errors.push('GO requires evidence_file');
  for (const dep of record.data.depends_on ?? []) {
    if (dep === 'MAIN-PRD') continue; // Parent is reading context, not the final release gate.
    if (byID.get(dep)?.data.delivery_decision !== 'GO') errors.push('Dependency not GO: ' + dep);
  }
  return errors;
}

export function validateEvidence(data, schema) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const validate = ajv.compile(schema);
  if (!validate(data)) return validate.errors.map(e => e.instancePath + ': ' + e.message);
  const errors = [];
  const ids = new Set();
  for (const check of data.checks) {
    if (ids.has(check.id)) errors.push('Duplicate check: ' + check.id);
    ids.add(check.id);
    if (data.status === 'PASS' && check.status !== 'PASS') errors.push('PASS evidence contains ' + check.status);
    if (path.isAbsolute(check.artifact) || check.artifact.split(/[\\/]/).includes('..'))
      errors.push('Evidence artifact must be a safe repository-relative path');
  }
  return errors;
}

export function isDeliveryEvidence(data) {
  return data.status === 'PASS' && ['unit', 'qualification', 'final'].includes(data.scope);
}

export function sourceDigest(root) {
  const names = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: root, encoding: 'utf8' })
    .split('\0').filter(Boolean).filter(f => !f.startsWith('docs/evidence/')).sort();
  const hash = crypto.createHash('sha256');
  for (const name of [...new Set(names)]) {
    const file = path.join(root, name);
    if (!fs.statSync(file).isFile()) continue;
    hash.update(name + '\0'); hash.update(fs.readFileSync(file)); hash.update('\0');
  }
  return hash.digest('hex');
}

export function verifyEvidenceFiles(data, root) {
  const errors = [];
  for (const check of data.checks) {
    const file = path.resolve(root, check.artifact);
    if (!file.startsWith(path.resolve(root) + path.sep)) { errors.push('Artifact escaped root'); continue; }
    if (!fs.existsSync(file)) { errors.push('Missing artifact: ' + check.artifact); continue; }
    const actual = crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
    if (actual !== check.sha256) errors.push('Artifact digest mismatch: ' + check.artifact);
  }
  return errors;
}

export function validateSupportMatrix(data) {
  const errors = [];
  if (data.schemaVersion !== 1 || data.valkeyTopology !== 'unsharded-single-primary') errors.push('Unsupported matrix or topology');
  for (const name of ['postgresql', 'valkey', 'kubernetes', 'docker', 'compose', 'helm']) {
    if (!/^\d+\.\d+(\.\d+)?$/.test(data.targets?.[name] ?? '')) errors.push('Exact version required: ' + name);
  }
  if (!['candidate-not-production-qualified', 'qualified'].includes(data.status)) errors.push('Invalid qualification status');
  if (data.status === 'qualified' && !data.evidence_file) errors.push('Qualified matrix needs evidence');
  return errors;
}

export function finalGate(records) {
  const byID = new Map(records.map(r => [r.data.id, r]));
  const errors = validateGraph(records);
  for (let i = 1; i <= 8; i++) {
    const id = 'SUB-PRD-' + String(i).padStart(2, '0');
    if (!byID.has(id) || byID.get(id).data.delivery_decision !== 'GO') errors.push(id + ' is NO-GO');
    else errors.push(...validateDelivery(byID.get(id), byID));
  }
  return errors;
}

// Arithmetic feasibility only: this does not certify achieved performance.
export function validateBenchmarkProfiles(data) {
  const errors = [];
  if (data.schemaVersion !== 1 || !Array.isArray(data.profiles) || data.profiles.length !== 2)
    return ['Two versioned benchmark profiles required'];
  const skew = data.measurement?.clockSkewSeconds;
  if (!Number.isFinite(skew) || skew < 0 || skew > 30) errors.push('Invalid verifier leeway');
  for (const p of data.profiles) {
    for (const key of ['visitorCap','idempotencyCap','leaseCap','admissionTTLSeconds','idempotencyTTLSeconds','ratePerMinute','joinPerSecond','claimPerSecond','peakClaimPerSecond','peakSeconds','steadySeconds']) {
      if (!Number.isSafeInteger(p[key]) || p[key] <= 0) errors.push(p.id + ': positive integer required: ' + key);
    }
    if (p.idempotencyTTLSeconds !== 600) errors.push(p.id + ': default idempotency retention required');
    if (p.joinPerSecond !== p.claimPerSecond) errors.push(p.id + ': steady replenishment must equal claims');
    if (p.joinPerSecond * Math.min(p.idempotencyTTLSeconds, p.steadySeconds) > p.idempotencyCap)
      errors.push(p.id + ': steady idempotency capacity exceeded');
    if (p.claimPerSecond * (p.admissionTTLSeconds + skew) > p.leaseCap || p.leaseCap > p.visitorCap)
      errors.push(p.id + ': steady lease capacity exceeded');
    if (p.claimPerSecond * 60 > p.ratePerMinute || p.peakClaimPerSecond * Math.min(60,p.peakSeconds) > p.ratePerMinute)
      errors.push(p.id + ': rolling rate exceeded');
    if (p.peakClaimPerSecond * p.peakSeconds > Math.min(p.visitorCap,p.idempotencyCap,p.leaseCap))
      errors.push(p.id + ': isolated peak capacity exceeded');
  }
  return errors;
}
