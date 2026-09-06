// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync, spawnSync } from 'node:child_process';
import YAML from 'yaml';
import { parsePRD, validateGraph, validateDelivery, validateEvidence, isDeliveryEvidence, verifyEvidenceFiles, sourceDigest, validateSupportMatrix, validateBenchmarkProfiles, finalGate } from './prd.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const files = ['main-prd.md', ...Array.from({length: 8}, (_, i) => 'sub-prd_' + String(i+1).padStart(2,'0') + '.md')];
const records = [], errors = [];
for (const file of files) {
  try {
    const text = fs.readFileSync(path.join(root, 'docs', file), 'utf8');
    const r = parsePRD(text); records.push(r);
    if (file.startsWith('sub-')) {
      for (const section of ['구현 Checklist', 'Unit test Checklist', 'Unit test 실행 로그', 'GO/NO-GO 판정'])
        if (!text.includes(section)) errors.push(file + ': missing ' + section);
      const ids = [...text.matchAll(/^\| (UT-\d+-\d+) \|/gm)].map(m=>m[1]);
      if (!ids.length || new Set(ids).size !== ids.length) errors.push(file + ': missing/duplicate UT IDs');
    }
    if ((text.match(/^```/gm) ?? []).length % 2) errors.push(file + ': unbalanced code fence');
    for (const match of text.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)) {
      const target = match[1].split('#')[0];
      if (/^https?:/.test(target) || !target) continue;
      if (!fs.existsSync(path.resolve(root,'docs',target))) errors.push(file + ': broken link ' + target);
    }
  } catch (e) { errors.push(file + ': ' + e.message); }
}
errors.push(...validateGraph(records));
const byID = new Map(records.map(r=>[r.data.id,r]));
const evidenceSchema = JSON.parse(fs.readFileSync(path.join(root,'test/fixtures/evidence.schema.json'),'utf8'));
for (const r of records) {
  errors.push(...validateDelivery(r,byID).map(e=>r.data.id+': '+e));
  if (r.data.delivery_decision === 'GO' && r.data.evidence_file) {
    try {
      const evidencePath = path.resolve(root,r.data.evidence_file);
      if (!evidencePath.startsWith(root + path.sep)) throw new Error('evidence_file escapes root');
      const ev=JSON.parse(fs.readFileSync(evidencePath,'utf8'));
      errors.push(...validateEvidence(ev,evidenceSchema),...verifyEvidenceFiles(ev,root));
      if (ev.sourceDigest!==sourceDigest(root)) errors.push(r.data.id+': stale source evidence');
      if (!isDeliveryEvidence(ev)) errors.push(r.data.id+': M0/local/non-PASS cannot prove delivery GO');
    } catch(e) { errors.push(r.data.id+': '+e.message); }
  }
}
const owners=YAML.parse(fs.readFileSync(path.join(root,'docs/requirements.yaml'),'utf8'));
for (const entry of owners.requirements) {
  const owner=byID.get(entry.owner);
  if (!owner || !owner.body.includes(entry.marker)) errors.push('Unknown requirement owner/marker: '+entry.id);
}
if(new Set(owners.requirements.map(x=>x.id)).size!==owners.requirements.length) errors.push('Duplicate requirement ownership');
errors.push(...validateSupportMatrix(YAML.parse(fs.readFileSync(path.join(root,'support-matrix.yaml'),'utf8'))));
errors.push(...validateBenchmarkProfiles(YAML.parse(fs.readFileSync(path.join(root,'docs/benchmarks/profiles.yaml'),'utf8'))));
for(const f of files) {
  const ignored=spawnSync('git',['check-ignore','--no-index','-q','docs/'+f],{cwd:root});
  if(ignored.status===0) errors.push('Git ignores docs/'+f);
  else if(ignored.status!==1) errors.push('Cannot verify Git ignore rules');
}
if(process.argv.includes('--final')) errors.push(...finalGate(records));
if(errors.length){ console.error(errors.map(x=>'NO-GO: '+x).join('\n')); process.exitCode=1; }
else console.log('PASS: 9 PRDs; ownership, dependency graph, checklist, links, evidence rules and Git tracking.');
