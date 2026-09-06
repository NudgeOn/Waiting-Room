// SPDX-License-Identifier: Apache-2.0
// Local PostgreSQL storage evidence, not an end-to-end Admin login or release gate.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {sourceDigest,validateEvidence,verifyEvidenceFiles} from './prd.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const runId=process.argv[2];
const passwordLogin=process.argv.includes('--password-login');
const sessions=process.argv.includes('--sessions');
const bootstrap=process.argv.includes('--bootstrap');
const enrollment=process.argv.includes('--enrollment');
const recovery=process.argv.includes('--recovery');
const authHTTP=process.argv.includes('--auth-http');
const provisioningHTTP=process.argv.includes('--provisioning-http');
const adminUI=process.argv.includes('--admin-ui');
if(!runId||!/^[a-zA-Z0-9_-]+$/.test(runId))throw new Error('Provide a unique safe run ID');
if(process.env.WR_TEST_AUTH_DB!=='local')throw new Error('WR_TEST_AUTH_DB=local required');
const folder=path.join(root,'docs/evidence',runId);
if(fs.existsSync(folder))throw new Error('Refusing to overwrite evidence');
const env={...process.env,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod')};
if(adminUI){env.PLAYWRIGHT_BROWSERS_PATH=path.join(root,'.cache/ms-playwright');env.WR_ADMIN_SCREEN_DIR=path.join(folder,'screenshots');}
const before=sourceDigest(root),startedAt=new Date().toISOString(),checks=[];
fs.mkdirSync(folder,{recursive:true});
const steps=[
  ['foundation','make',['check']],
  ['admin-unit','go',['test','-race','-count=1','-v','./internal/adminauth/...']],
  ...(adminUI?[
    ['admin-lab-unit','go',['test','-race','-count=1','-v','./internal/adminlab']],
    ['admin-browser','npm',['run','test:admin-browser','--','--repeat-each=3']]
  ]:[]),
  ['postgres-integration','make',['test-auth-db']],
  ['postgres-repeat','go',['test','-tags=integration','-race','-count=10','-v','-run','^TestPostgres','./internal/adminauth/pgstore']],
  ...(authHTTP?[
    ['auth-http-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestAuthHTTP','./internal/adminauth/authhttp','./internal/adminauth/pgstore']]
  ]:[]),
  ...(provisioningHTTP?[
    ['provisioning-http-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestProvisioning','./internal/adminauth/authhttp']]
  ]:[]),
  ...(recovery?[
    ['recovery-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestRecovery','./internal/adminauth/pgstore']]
  ]:[]),
  ...(enrollment?[
    ['enrollment-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestEnrollment','./internal/adminauth/pgstore']]
  ]:[]),
  ...(bootstrap?[
    ['bootstrap-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestBootstrap','./internal/adminauth/pgstore']]
  ]:[]),
  ...(sessions?[
    ['session-repeat','go',['test','-tags=integration','-race','-count=10','-v','-run','^TestSession','./internal/adminauth/pgstore','./internal/adminauth/sessionhttp']]
  ]:[]),
  ...(passwordLogin?[
    ['password-repeat','go',['test','-tags=integration','-race','-count=3','-v','-run','^TestPassword','./internal/adminauth/pgstore']],
    ['password-fuzz','go',['test','-run','^$','-fuzz','^FuzzPasswordEncoding$','-fuzztime=5s','-parallel=2','./internal/adminauth']],
    ['password-calibration','make',['auth-calibrate']]
  ]:[]),
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
  postgresql:'17.11; exact server version asserted by integration tests',
  databaseScope:'dedicated loopback 15432; randomly named per-test schemas cleaned; two independent pools in one process',
  restart:'NOT_RUN',unknownCommit:'UNIT_DOUBLE_ONLY',
  passwordChallenge:passwordLogin?'ARGON2ID_VERIFIED_FIXTURE_ACCOUNT':'NOT_ASSERTED_BY_THIS_BUNDLE',
  passwordFixtureIterations:passwordLogin?2:null,
  adminHTTP:authHTTP?'PASSWORD_OTP_RECOVERY_OFF_ME_LOGOUT_TEMPORARY_TLS_HTTP1_HTTP2':sessions?'ME_LOGOUT_ONLY_TEMPORARY_TLS_TEST':'NOT_ASSERTED_BY_THIS_BUNDLE',
  provisioningHTTP:provisioningHTTP?'BOOTSTRAP_ON_OFF_ENROLL_REAL_RECOVERY_TLS_HTTP1_HTTP2':'NOT_ASSERTED_BY_THIS_BUNDLE',
  sessionSeed:authHTTP?'HTTP_LOGIN_COOKIE_FROM_SQL_FIXTURE_ACCOUNT':sessions?'SQL_FIXTURE_NOT_HTTP_LOGIN':null,
  sessionTouch:sessions?'INTERNAL_SERVICE_ONLY':null,
  bootstrap:bootstrap?'SERVICE_ONLY_LOOPBACK_FIXTURE_NO_HTTP_OR_CLI':'NOT_ASSERTED_BY_THIS_BUNDLE',
  enrollment:enrollment?'SERVICE_SECRET_OTP_COMMIT_RECOVERY_HASHES_NO_HTTP_QR_OR_RECOVERY_LOGIN':bootstrap?'RESTRICTED_TOKEN_ONLY_NO_SECRET_QR_VERIFY_OR_RECOVERY':'NOT_ASSERTED_BY_THIS_BUNDLE',
  recovery:recovery?'PASSWORD_CHALLENGE_RECOVERY_SERVICE_NO_HTTP_OR_UI':'NOT_ASSERTED_BY_THIS_BUNDLE',
  backofficeUI:adminUI?'LOCAL_AUTH_ONLY_REACT_NOT_ROOM_OPERATIONS':'NOT_ASSERTED_BY_THIS_BUNDLE',browser:adminUI?'CHROMIUM_SELF_SIGNED_TLS_EXCEPTION_LOCAL_ONLY':'NOT_RUN_THIS_RUN',
  qualification:'NOT_RUN',final:'NOT_RUN',remoteCI:'NOT_RUN'
},null,2)+'\n');
console.log('Admin PostgreSQL storage local '+report.status);
if(report.status!=='PASS')process.exitCode=1;
