// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';

// Only the caller's newly created project/network/volume can be selected.
export function valkeyObserver(project,image) {
  assert.match(project,/^waiting-room-beta-tiers-[a-f0-9]{8}$/);
  assert.match(image,/^waiting-room-[a-z0-9._-]+:local$/);
  const name=project+'-latency-observer';let started=false;
  function run(...args) {return spawnSync('docker',args,{encoding:'utf8',timeout:15000,maxBuffer:1024*1024});}
  return {
    async start() {
      const out=run('run','-d','--rm','--name',name,'--network',project+'_queue','--user','65532:65532','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges',
        '--mount',`type=volume,src=${project}_state,dst=/state,readonly`,
        '--mount',`type=bind,src=${path.resolve('build/beta8-valkey-observer/probe')},dst=/probe,readonly`,
        '--entrypoint','/probe',image);
      assert.equal(out.status,0,'isolated latency observer starts; details suppressed');started=true;
      for(let i=0;i<20;i++) {await delay(100);const logs=run('logs',name);if(logs.stdout.includes('"event":"latency_monitor_enabled"')) {console.log('PASS: disposable fixture latency observer enabled at 100ms; durability/TTL/quota unchanged');return;}if(logs.status!==0||logs.stdout.includes('"error":'))break;}
      throw Error('isolated latency observer did not become ready');
    },
    finish() {
      if(!started)return;started=false;
      const logs=run('logs',name);
      if(logs.status===0)for(const line of logs.stdout.split('\n'))if(line.startsWith('{')) {try{const row=JSON.parse(line);if(['latency_monitor_enabled','valkey_sample','valkey_latency'].includes(row.event))console.log('VALKEY OBSERVER: '+JSON.stringify(row));}catch{}}
      if(run('inspect',name).status===0) {const stopped=run('stop','--time','5',name);assert.equal(stopped.status,0,'isolated latency observer stops before fixture network cleanup');}
    }
  };
}
