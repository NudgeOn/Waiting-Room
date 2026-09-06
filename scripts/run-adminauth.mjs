// SPDX-License-Identifier: Apache-2.0
// Library-only evidence: never mistaken for deployed Admin authentication.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {sourceDigest,validateEvidence,verifyEvidenceFiles} from './prd.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const runId=process.argv[2];
if(!runId||!/^[a-zA-Z0-9_-]+$/.test(runId))throw new Error('Provide a unique safe run ID');
const folder=path.join(root,'docs/evidence',runId);
if(fs.existsSync(folder))throw new Error('Refusing to overwrite evidence');
const env={...process.env,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod')};
const before=sourceDigest(root),startedAt=new Date().toISOString(),checks=[];
fs.mkdirSync(folder,{recursive:true});
const steps=[
  ['foundation','make',['check']],
  ['admin-auth','go',['test','-race','-count=1','-v','./internal/adminauth']],
  ['totp-fuzz','go',['test','-run','^$','-fuzz','^FuzzTOTPInput$','-fuzztime=10s','-parallel=2','./internal/adminauth']],
  ['final-guard','make',['test-final']]
];
for(const[id,bin,args]of steps){
  console.log('Running '+id);
  const result=spawnSync(bin,args,{cwd:root,env,encoding:'utf8',timeout:180000,maxBuffer:20*1024*1024});
  const output=(result.stdout??'')+(result.stderr??'')+(result.error?.message??'');
  const artifact='docs/evidence/'+runId+'/'+id+'.log';
  fs.writeFileSync(path.join(root,artifact),output);
  const expected=id==='final-guard';
  const passed=expected?result.status===2&&Array.from({length:8},(_,i)=>'SUB-PRD-'+String(i+1).padStart(2,'0')+' is NO-GO').every(s=>output.includes(s)):result.status===0;
  checks.push({id,status:passed?'PASS':'FAIL',command:[bin,...args].join(' ')+(expected?' [expected NO-GO exit 2]':''),artifact,sha256:crypto.createHash('sha256').update(output).digest('hex')});
  console.log(id+': '+checks.at(-1).status);
}
const unchanged=before===sourceDigest(root);
const report={schemaVersion:1,scope:'local',runId,sourceDigest:before,status:unchanged&&checks.every(c=>c.status==='PASS')?'PASS':'FAIL',checks};
const errors=[...validateEvidence(report,JSON.parse(fs.readFileSync(path.join(root,'test/fixtures/evidence.schema.json')))),...verifyEvidenceFiles(report,root)];
if(errors.length)throw new Error(errors.join('\n'));
fs.writeFileSync(path.join(folder,'report.json'),JSON.stringify(report,null,2)+'\n');
fs.writeFileSync(path.join(folder,'environment.json'),JSON.stringify({
  startedAt,finishedAt:new Date().toISOString(),sourceDigest:before,sourceUnchanged:unchanged,
  commit:null,uncommittedSnapshot:true,go:spawnSync('go',['version'],{env,encoding:'utf8'}).stdout.trim(),
  node:process.version,os:process.platform,arch:process.arch,
  counterStore:'unit-only mutex fixture',postgresql:'NOT_IMPLEMENTED',adminHTTP:'NOT_IMPLEMENTED',
  backofficeUI:'NOT_IMPLEMENTED',browser:'NOT_RUN_THIS_RUN',qualification:'NOT_RUN',final:'NOT_RUN'
},null,2)+'\n');
console.log('Admin auth core local '+report.status);
if(report.status!=='PASS')process.exitCode=1;
