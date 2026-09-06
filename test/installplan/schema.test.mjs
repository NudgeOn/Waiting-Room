// SPDX-License-Identifier: Apache-2.0
import {test, before, after} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import Ajv2020 from 'ajv/dist/2020.js';

const validate = new Ajv2020({strict:true, strictTypes:false}).compile(JSON.parse(fs.readFileSync('api/schema/install-plan-input-v1.json')));
let folder, binary;
before(()=>{
  folder=fs.mkdtempSync(path.join(os.tmpdir(),'wr-plan-contract-'));
  binary=path.join(folder,'wrctl');
  const build=spawnSync('go',['build','-o',binary,'./cmd/wrctl'],{encoding:'utf8',timeout:120000,env:{...process.env,GOCACHE:path.resolve('.cache/go-build'),GOMODCACHE:path.resolve('.cache/go-mod')}});
  assert.equal(build.status,0,build.stderr);
});
after(()=>{if(folder)fs.rmSync(folder,{recursive:true,force:true});});

for(const profile of ['standard','high-scale']) {
  test(`${profile}: schema and executable agree on profile/TOTP/region bounds`,()=>{
    const base=JSON.parse(fs.readFileSync(`test/installplan/${profile}.json`));
    const cap=profile==='standard'?10000:100000,rate=cap*0.6;
    const cases=[
      [true,()=>{}],
      [true,x=>{x.totp={mode:'configurable',enabled:false};}],
      [true,x=>{x.limits={maxActiveAdmissionLeases:cap,admissionsPerMinute:rate,admissionTtlSeconds:60};}],
      [true,x=>{x.limits.admissionTtlSeconds=3600;}],
      [false,x=>{x.expectedPeakVisitors=cap+1;}],
      [false,x=>{x.limits.maxActiveAdmissionLeases=cap+1;}],
      [false,x=>{x.limits.admissionsPerMinute=rate+1;}],
      [false,x=>{x.limits.admissionTtlSeconds=59;}],
      [false,x=>{x.limits.admissionTtlSeconds=3601;}],
      [false,x=>{x.totp={mode:'forced_on',enabled:false};}],
      [false,x=>{delete x.totp.enabled;}],
      [false,x=>{x.totp.enabled=null;}],
      [false,x=>{x.queuePolicy='lottery';}],
      [false,x=>{x.regionId=['seoul','tokyo'];}],
      [false,x=>{x.regions=['seoul','tokyo'];}],
      [false,x=>{x.regionId='seoul,tokyo';}],
      [false,x=>{x.regionId='seoul\n';}],
      [false,x=>{x.password='DO-NOT-ECHO';}],
      [false,x=>{x.totp.secret='DO-NOT-ECHO';}],
      [false,x=>{x.Profile=x.profile;delete x.profile;}],
      [false,x=>{x.schemaVersion=2;}],
    ];
    for(const [expected,mutate] of cases){
      const input=structuredClone(base);mutate(input);
      assert.equal(validate(input),expected,JSON.stringify(validate.errors));
      const result=spawnSync(binary,['plan'],{input:JSON.stringify(input),encoding:'utf8',timeout:10000});
      assert.equal(result.status,expected?0:2,result.stderr);
      assert.ok(!result.stderr.includes('DO-NOT-ECHO'));
      if(!expected)assert.equal(result.stdout,'');
      else {const plan=JSON.parse(result.stdout);assert.equal(plan.activationAllowed,false);assert.equal(plan.executable,false);}
    }
  });
}

test('cost: schema and CLI agree; exact subtotals preserve provenance',()=>{
  const ajv=new Ajv2020({strict:true});
  ajv.addFormat('date',s=>{
    if(!/^[2-9][0-9]{3}-[0-9]{2}-[0-9]{2}$/.test(s))return false;
    const d=new Date(s+'T00:00:00.000Z');
    return !Number.isNaN(d.getTime())&&d.toISOString().slice(0,10)===s;
  });
  const valid=ajv.compile(JSON.parse(fs.readFileSync('api/schema/cost-input-v1.json')));
  const base=JSON.parse(fs.readFileSync('test/installplan/cost-example.json'));
  const cases=[
    [true,()=>{}],
    [true,x=>{x.standardHostHourly='0';x.fractionDigits=0;}],
    [true,x=>{x.standardHostHourly='999999999.999999';x.highWorkerVolumeGiB=65536;x.monthlyHours=744;}],
    [true,x=>{x.asOf='2028-02-29';x.fractionDigits=4;}],
    [false,x=>{x.asOf='2026-02-29';}],
    [false,x=>{x.asOf='1999-12-31';}],
    [false,x=>{x.currency='usd';}],
    [false,x=>{x.currency='USD\n';}],
    [false,x=>{x.fractionDigits=5;}],
    [false,x=>{delete x.fractionDigits;}],
    [false,x=>{x.fractionDigits=null;}],
    [false,x=>{x.monthlyHours=0;}],
    [false,x=>{x.monthlyHours=745;}],
    [false,x=>{x.standardHostHourly=0.1;}],
    [false,x=>{x.standardHostHourly='-1';}],
    [false,x=>{x.standardHostHourly='1e3';}],
    [false,x=>{x.standardHostHourly='1.0000001';}],
    [false,x=>{x.standardHostHourly='1\n';}],
    [false,x=>{x.highWorkerVolumeGiB=0;}],
    [false,x=>{x.expectedEgressGiB=-1;}],
    [false,x=>{x.password='DO-NOT-ECHO';}],
    [false,x=>{x.regionId='a,b';}],
    [false,x=>{x.Provider=x.provider;delete x.provider;}],
  ];
  for(const [expected,mutate]of cases){
    const input=structuredClone(base);mutate(input);
    assert.equal(valid(input),expected,JSON.stringify(valid.errors));
    const result=spawnSync(binary,['estimate'],{input:JSON.stringify(input),encoding:'utf8',timeout:10000});
    assert.equal(result.status,expected?0:2,result.stderr);
    assert.ok(!result.stderr.includes('DO-NOT-ECHO'));
    if(!expected)assert.equal(result.stdout,'');
    else {const r=JSON.parse(result.stdout);assert.deepEqual(r.input,input);assert.equal(r.qualification,'NOT_RUN');assert.equal(r.profiles.length,2);}
  }
});

test('report: nested input schemas, CLI recomputation and region equality',()=>{
  const ajv=new Ajv2020({strict:true,strictTypes:false});
  ajv.addFormat('date',s=>{const d=new Date(s+'T00:00:00.000Z');return /^[2-9][0-9]{3}-[0-9]{2}-[0-9]{2}$/.test(s)&&!Number.isNaN(d.getTime())&&d.toISOString().slice(0,10)===s;});
  for(const file of ['install-plan-input-v1','cost-input-v1'])ajv.addSchema(JSON.parse(fs.readFileSync(`api/schema/${file}.json`)));
  const valid=ajv.compile(JSON.parse(fs.readFileSync('api/schema/report-input-v1.json')));
  const base=JSON.parse(fs.readFileSync('test/installplan/report-example.json'));
  const cases=[
    [true,true,()=>{}],[true,true,x=>{delete x.cost;}],
    [false,false,x=>{x.cost=null;}],[false,false,x=>{x.activationAllowed=true;}],
    [false,false,x=>{x.cost.total=0;}],[false,false,x=>{x.plan.totp.secret='DO-NOT-ECHO';}],
    [false,false,x=>{x.plan.expectedPeakVisitors=10001;}],
    [true,false,x=>{x.cost.regionId='different-region';}],
  ];
  for(const [schemaExpected,cliExpected,mutate]of cases){const input=structuredClone(base);mutate(input);assert.equal(valid(input),schemaExpected);const r=spawnSync(binary,['report'],{input:JSON.stringify(input),encoding:'utf8',timeout:10000});assert.equal(r.status,cliExpected?0:2);assert.ok(!r.stderr.includes('DO-NOT-ECHO'));if(cliExpected){const out=JSON.parse(r.stdout);assert.equal(out.payload.installation,'NOT_RUN');assert.equal(out.payload.activationAllowed,false);}else assert.equal(r.stdout,'');}
});
