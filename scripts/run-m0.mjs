// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {sourceDigest,validateEvidence,verifyEvidenceFiles} from './prd.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const runId=process.argv[2]??new Date().toISOString().replace(/[:.]/g,'-');
if(!/^[a-zA-Z0-9_-]+$/.test(runId))throw new Error('Run ID must be a safe filename');
const folder=path.join(root,'docs/evidence',runId);
if(fs.existsSync(folder))throw new Error('Evidence run already exists; choose a new run ID');
fs.mkdirSync(folder,{recursive:true});
const start=new Date().toISOString();
const before=sourceDigest(root);
const steps=[
  ['format','make',['fmt-check']],
  ['vet','make',['vet']],
  ['go-race','go',['test','-race','-count=1','-json','./...']],
  ['tooling-unit','npm',['test']],
  ['prd-validation','make',['check-docs']],
  ['openapi-lint','make',['lint-api']],
  ['contract','make',['test-contract']],
  ['final-guard','make',['test-final']]
];
const checks=[];
for(const[id,bin,args]of steps){
  const result=spawnSync(bin,args,{cwd:root,encoding:'utf8',env:{...process.env,GOCACHE:path.join(root,'.cache/go-build')},maxBuffer:20*1024*1024});
  const output=(result.stdout??'')+(result.stderr??'')+(result.error?.message??'');
  const artifact='docs/evidence/'+runId+'/'+id+'.log';
  fs.writeFileSync(path.join(root,artifact),output);
  const passed=id==='final-guard'
    ? result.status===2 && Array.from({length:8},(_,i)=>'SUB-PRD-'+String(i+1).padStart(2,'0')+' is NO-GO').every(x=>output.includes(x))
    : result.status===0;
  checks.push({id,status:passed?'PASS':'FAIL',command:[bin,...args].join(' ')+(id==='final-guard'?' [expect exit 2 and all eight child NO-GO reasons]':''),artifact,sha256:crypto.createHash('sha256').update(output).digest('hex')});
  console.log(id+': '+checks.at(-1).status);
}
const after=sourceDigest(root);
const report={schemaVersion:1,scope:'m0',runId,sourceDigest:before,status:checks.every(x=>x.status==='PASS')&&before===after?'PASS':'FAIL',checks};
const schema=JSON.parse(fs.readFileSync(path.join(root,'test/fixtures/evidence.schema.json')));
const validation=[...validateEvidence(report,schema),...verifyEvidenceFiles(report,root)];
if(validation.length)throw new Error(validation.join('\n'));
fs.writeFileSync(path.join(folder,'report.json'),JSON.stringify(report,null,2)+'\n');
const git=spawnSync('git',['rev-parse','--verify','HEAD'],{cwd:root,encoding:'utf8'});
fs.writeFileSync(path.join(folder,'environment.json'),JSON.stringify({startedAt:start,finishedAt:new Date().toISOString(),commit:git.status===0?git.stdout.trim():null,uncommittedSnapshot:true,node:process.version,platform:process.platform,architecture:process.arch,go:spawnSync('go',['version'],{encoding:'utf8'}).stdout.trim(),sourceDigest:before,sourceUnchanged:before===after,qualification:'NOT_RUN',final:'NOT_RUN'},null,2)+'\n');
console.log('M0 '+report.status+': '+path.relative(root,path.join(folder,'report.json')));
if(report.status!=='PASS')process.exitCode=1;
