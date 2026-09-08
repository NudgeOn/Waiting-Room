// SPDX-License-Identifier: Apache-2.0
import {setTimeout as delay} from 'node:timers/promises';

const roles=['postgres','valkey','control','gateway','demo-origin','coordinator'];
export function runtimeReady(raw,elapsed){
  let services;
  try{const text=raw.trim();services=text.startsWith('[')?JSON.parse(text):text.split('\n').filter(Boolean).map(row=>JSON.parse(row));}catch{throw Error('Runtime health status unavailable');}
  if(!Array.isArray(services))throw Error('Runtime health status unavailable');
  let ready=true;
  for(const role of roles){
    const matches=services.filter(service=>service?.Service===role);
    if(matches.length>1)throw Error('Duplicate runtime service');
    const service=matches[0];
    if(!service){if(elapsed>=180000)throw Error('Required runtime service is missing');ready=false;continue;}
    if(service.State==='running'&&service.Health==='healthy')continue;
    ready=false;
    if(['dead','exited'].includes(service.State)||elapsed>=(role==='coordinator'?3700000:180000))throw Error('Runtime service did not become ready');
  }
  return ready;
}

export async function waitForRuntime(readStatus,{timeout=3700000,interval=3000}={}){
  const start=Date.now();
  while(Date.now()-start<timeout){
    if(runtimeReady(readStatus(),Date.now()-start))return;
    await delay(interval);
  }
  throw Error('Runtime readiness timed out; data retained');
}
