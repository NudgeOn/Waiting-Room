// SPDX-License-Identifier: Apache-2.0
// Planning/diagnostic evidence only: never applies resources or starts databases.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {sourceDigest,validateEvidence,verifyEvidenceFiles} from './prd.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const runId=process.argv[2];
const previewUI=process.argv.includes('--preview-ui');
const clockLocal=process.argv.includes('--clock-local');
if(process.argv.slice(3).some(x=>!['--preview-ui','--clock-local'].includes(x)))throw new Error('Unknown runner option');
if(!runId||!/^[a-zA-Z0-9_-]+$/.test(runId))throw new Error('Provide a unique safe run ID');
const folder=path.join(root,'docs/evidence',runId);
if(fs.existsSync(folder))throw new Error('Refusing to overwrite evidence');
const env={...process.env,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod'),PLAYWRIGHT_BROWSERS_PATH:path.join(root,'.cache/ms-playwright')};
const before=sourceDigest(root),startedAt=new Date().toISOString(),checks=[];
fs.mkdirSync(folder,{recursive:true});
const steps=[
  ['foundation','make',['check']],
  ['install-plan-unit','go',['test','-race','-count=1','-json','./internal/installplan/...','./cmd/wrctl']],
  ['install-plan-repeat','go',['test','-race','-count=3','./internal/installplan/...','./cmd/wrctl']],
  ['schema-cli-contract','make',['test-install-plan-schema']],
  ['input-fuzz','go',['test','-run','^$','-fuzz','^FuzzDecode$','-fuzztime=10s','-parallel=2','./internal/installplan']],
  ['clock-fuzz','go',['test','-run','^$','-fuzz','^FuzzClockFailClosed$','-fuzztime=10s','-parallel=2','./internal/installplan/preflight']],
  ['cost-fuzz','go',['test','-run','^$','-fuzz','^FuzzCostArithmetic$','-fuzztime=10s','-parallel=2','./internal/installplan']],
  ['report-fuzz','go',['test','-run','^$','-fuzz','^FuzzReportDecode$','-fuzztime=10s','-parallel=2','./internal/installplan']],
  ['tracking-fuzz','go',['test','-run','^$','-fuzz','^FuzzTracking$','-fuzztime=10s','-parallel=2','./internal/installplan/clockcheck']],
  ['final-guard','make',['test-final']]
];
if(previewUI)steps.splice(4,0,['preview-browser','npm',['run','test:preview-browser','--','--repeat-each=2']]);
if(clockLocal)steps.splice(4,0,['clock-local','node',['scripts/check-clock-local.mjs']]);
for(const[id,bin,args]of steps){
  console.log('Running '+id);
  const result=spawnSync(bin,args,{cwd:root,env,encoding:'utf8',timeout:180000,maxBuffer:20*1024*1024});
  const output=(result.stdout??'')+(result.stderr??'')+(result.error?.message??'');
  const artifact='docs/evidence/'+runId+'/'+id+'.log';
  fs.writeFileSync(path.join(root,artifact),output);
  const expected=id==='final-guard';
  const passed=expected?result.status===2&&Array.from({length:8},(_,i)=>'SUB-PRD-'+String(i+1).padStart(2,'0')+' is NO-GO').every(s=>output.includes(s)):result.status===0;
  checks.push({id,status:passed?'PASS':'FAIL',command:[bin,...args].join(' ')+(expected?' [expected NO-GO exit 2; no final integration]':''),artifact,sha256:crypto.createHash('sha256').update(output).digest('hex')});
  console.log(id+': '+checks.at(-1).status);
}
const unchanged=before===sourceDigest(root);
const report={schemaVersion:1,scope:'local',runId,sourceDigest:before,status:unchanged&&checks.every(c=>c.status==='PASS')?'PASS':'FAIL',checks};
const errors=[...validateEvidence(report,JSON.parse(fs.readFileSync(path.join(root,'test/fixtures/evidence.schema.json')))),...verifyEvidenceFiles(report,root)];
if(errors.length)throw new Error(errors.join('\n'));
fs.writeFileSync(path.join(folder,'report.json'),JSON.stringify(report,null,2)+'\n');
fs.writeFileSync(path.join(folder,'environment.json'),JSON.stringify({startedAt,finishedAt:new Date().toISOString(),sourceDigest:before,sourceUnchanged:unchanged,commit:null,uncommittedSnapshot:true,go:spawnSync('go',['version'],{env,encoding:'utf8'}).stdout.trim(),node:process.version,os:process.platform,arch:process.arch,scope:clockLocal?'planning and read-only local clock diagnostic':'offline plan only',clock:clockLocal?'SEE_CLOCK_LOCAL_LOG_NOT_QUALIFICATION':'NOT_RUN',database:'NOT_RUN',browser:previewUI?'CHROMIUM_LOOPBACK_PREVIEW':'NOT_RUN_THIS_RUN',apply:'NOT_IMPLEMENTED',qualification:'NOT_RUN',final:'NOT_RUN'},null,2)+'\n');
console.log('Install plan local '+report.status);
if(report.status!=='PASS')process.exitCode=1;
