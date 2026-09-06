// SPDX-License-Identifier: Apache-2.0
// Explicit local read-only diagnostic smoke; never equate a passed boundary
// test with a synchronized host. Only the sanitized CLI result is printed.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawnSync} from 'node:child_process';

const folder=fs.mkdtempSync(path.join(os.tmpdir(),'wr-clock-local-'));
try {
  const binary=path.join(folder,'wrctl');
  const env={...process.env,GOCACHE:path.resolve('.cache/go-build'),GOMODCACHE:path.resolve('.cache/go-mod')};
  const build=spawnSync('go',['build','-o',binary,'./cmd/wrctl'],{env,encoding:'utf8',timeout:120000});
  assert.equal(build.status,0,build.stderr);
  const result=spawnSync(binary,['doctor-clock'],{input:fs.readFileSync('test/installplan/standard.json'),encoding:'utf8',timeout:10000,maxBuffer:65536});
  assert.ok([0,3].includes(result.status),'unexpected CLI failure');
  assert.equal(result.stderr,'');
  const report=JSON.parse(result.stdout);
  assert.equal(report.scope,'local-clock-diagnostic-only');
  assert.equal(report.policy.activationAllowed,false);
  assert.equal(report.policy.qualification,'NOT_RUN');
  assert.equal(report.policy.hasBlockingChecks,true);
  const clock=report.policy.checks.find(c=>c.id==='clock');
  assert.ok(clock);
  assert.equal(result.status,['PASS','WARN'].includes(clock.status)?0:3);
  if(process.platform!=='linux') {
    assert.equal(report.collectorCode,'CLOCK_PLATFORM_UNSUPPORTED');
    assert.equal(clock.status,'UNVERIFIED');
    assert.equal(report.artifact,undefined);
  }
  console.log(JSON.stringify({boundaryTest:'PASS',hostClockStatus:clock.status,exitCode:result.status,platform:process.platform,report},null,2));
} finally {
  fs.rmSync(folder,{recursive:true,force:true});
}
