// SPDX-License-Identifier: Apache-2.0
// Run against the dedicated local Compose instance; persistence step restarts that instance.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import os from 'node:os';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {sourceDigest,validateEvidence,verifyEvidenceFiles} from './prd.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const runId=process.argv[2];
const browser=process.argv.includes('--browser');
const processes=process.argv.includes('--processes');
const tiers=process.argv.includes('--tiers');
const httpTiers=process.argv.includes('--http-tiers');
const fuzz=process.argv.includes('--fuzz');
if(process.argv.slice(3).some(x=>!['--browser','--processes','--tiers','--http-tiers','--fuzz'].includes(x)))throw new Error('Unknown runner option');
if(!runId||!/^[a-zA-Z0-9_-]+$/.test(runId))throw new Error('Provide a unique safe run ID');
const folder=path.join(root,'docs/evidence',runId);
if(fs.existsSync(folder))throw new Error('Refusing to overwrite evidence');
const env={...process.env,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod'),WR_TEST_VALKEY:'127.0.0.1:16379',WR_TEST_RESTART_CONTAINER:'waiting-room-m1-valkey-1'};
const inspect=spawnSync('docker',['inspect','--format','{{index .Config.Labels "waiting-room.scope"}}','waiting-room-m1-valkey-1'],{cwd:root,env,encoding:'utf8'});
if(inspect.status!==0||inspect.stdout.trim()!=='m1-local')throw new Error('Dedicated Compose lab required; run make lab-valkey');
fs.mkdirSync(folder,{recursive:true});
const before=sourceDigest(root),startedAt=new Date().toISOString(),checks=[];
const steps=[['foundation','make',['check']],['integration','make',['test-integration']],['restart','make',['test-persistence']],['quick20','make',['lab-quick']],['final-guard','make',['test-final']]];
steps.splice(1,0,['public-lab-unit','go',['test','-race','-count=1','-json','./internal/lab/...','./internal/admission/...','./internal/waiting/...']]);
steps.splice(2,0,['config-trust-unit','go',['test','-race','-count=1','-json','./internal/configtrust/...','./internal/processlab/...']]);
if(processes)steps.splice(2,0,['process-integration','make',['test-processes']],['process-quick20','make',['process-lab-quick']]);
if(tiers)steps.splice(2,0,['visitor-tiers','make',['test-local-tiers']]);
if(httpTiers)for(let attempt=1;attempt<=3;attempt++)steps.splice(1+attempt,0,['http-visitor-tiers-'+attempt,'make',['test-http-tiers']]);
if(fuzz)steps.splice(2,0,['fuzz-input','make',['test-fuzz-input']],['fuzz-config','make',['test-fuzz-config']],['fuzz-control','make',['test-fuzz-control']]);
if(browser){
  env.WR_SCREENSHOTS=fs.mkdtempSync(path.join(os.tmpdir(),'wr-calm-'));
  steps.splice(4,0,['browser','make',['test-browser']]);
}
for(const[id,bin,args]of steps){
  console.log('Running '+id);
  const result=spawnSync(bin,args,{cwd:root,env,encoding:'utf8',timeout:180000,maxBuffer:20*1024*1024});
  const output=(result.stdout??'')+(result.stderr??'')+(result.error?.message??'');
  const artifact='docs/evidence/'+runId+'/'+id+'.log';
  fs.writeFileSync(path.join(root,artifact),output);
  const expected=id==='final-guard';
  const passed=expected ? result.status===2&&Array.from({length:8},(_,i)=>'SUB-PRD-'+String(i+1).padStart(2,'0')+' is NO-GO').every(s=>output.includes(s)) : result.status===0;
  checks.push({id,status:passed?'PASS':'FAIL',command:[bin,...args].join(' ')+(expected?' [expected NO-GO exit 2]':''),artifact,sha256:crypto.createHash('sha256').update(output).digest('hex')});
  console.log(id+': '+checks.at(-1).status);
}
const unchanged=before===sourceDigest(root);
const report={schemaVersion:1,scope:'local',runId,sourceDigest:before,status:unchanged&&checks.every(c=>c.status==='PASS')?'PASS':'FAIL',checks};
const errors=[...validateEvidence(report,JSON.parse(fs.readFileSync(path.join(root,'test/fixtures/evidence.schema.json')))),...verifyEvidenceFiles(report,root)];
if(errors.length)throw new Error(errors.join('\n'));
fs.writeFileSync(path.join(folder,'report.json'),JSON.stringify(report,null,2)+'\n');
const command=(bin,args)=>spawnSync(bin,args,{cwd:root,env,encoding:'utf8'}).stdout?.trim();
fs.writeFileSync(path.join(folder,'environment.json'),JSON.stringify({startedAt,finishedAt:new Date().toISOString(),sourceDigest:before,sourceUnchanged:unchanged,commit:null,uncommittedSnapshot:true,go:command('go',['version']),node:process.version,os:process.platform,arch:process.arch,valkeyVersion:command('docker',['exec','waiting-room-m1-valkey-1','valkey-server','--version']),imageID:command('docker',['inspect','--format','{{.Image}}','waiting-room-m1-valkey-1']),docker:command('docker',['version','--format','{{.Server.Version}}']),compose:command('docker',['compose','version','--short']),qualification:'NOT_RUN',final:'NOT_RUN',processes:processes?'four local child PIDs; same OS user; not production isolation':'NOT_RUN',browser:browser?checks.find(c=>c.id==='browser').status:'NOT_RUN',browserScope:browser?'local Chromium; 1448x1086 and 360x800':null,screenshots:env.WR_SCREENSHOTS??null,backoffice:'NOT_TESTED_BY_M1_RUNNER; see separate Admin/Docker evidence'},null,2)+'\n');
console.log('M1 local '+report.status);
if(report.status!=='PASS')process.exitCode=1;
