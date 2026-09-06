// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {aggregateTelemetry,preferNewerDelivery,roomTelemetry} from '../src/telemetry.js';

const now=Date.parse('2026-09-06T08:00:00.000Z');
function fixture(){const runtime={revision:4,epoch:1,mode:'AUTO',limits:{admissionsPerMinute:600}};const metric={roomId:'sale',revision:4,epoch:1,mode:'AUTO',waiting:18,ready:3,leases:8,rate:12,originHealthy:false,arrivalWindowReady:false,arrivalsFiveMinutes:0,http5xxLastMinute:null,http5xxWindowReady:false};return {generation:9,state:'applied',config:{rooms:[{id:'sale',name:'판매'}]},runtimes:[{roomId:'sale',runtime}],nodes:[{id:'coordinator',generation:9,fresh:true,observedAt:new Date(now-1000).toISOString(),rooms:[{...metric}]},{id:'gateway',generation:9,fresh:true,observedAt:new Date(now-2000).toISOString(),rooms:[{...metric,waiting:0,ready:0,leases:0,rate:0,originHealthy:true,http5xxLastMinute:0,http5xxWindowReady:true}]}]};}
test('queue metrics use only Coordinator and subtract READY from active leases for ADMITTED',()=>{
  const result=roomTelemetry(fixture(),'sale',{now});assert.equal(result.waiting,18);assert.equal(result.ready,3);assert.equal(result.admitted,5);assert.equal(result.rate,12);assert.equal(result.originHealthy,true);assert.equal(result.applied,true);assert.equal(result.http5xx,0);
});
test('stale, missing, wrong-generation and mismatched revision observations cannot appear current',()=>{
  for(const mutate of [view=>{view.nodes[0].fresh=false;},view=>{view.nodes[0].observedAt=new Date(now-15001).toISOString();},view=>{view.nodes[0].generation=8;},view=>{view.nodes[0].rooms[0].revision=3;},view=>{view.nodes[0].rooms[0].epoch=2;},view=>{view.nodes.shift();}]){const view=fixture();mutate(view);const result=roomTelemetry(view,'sale',{now});assert.equal(result.waiting,null);assert.equal(result.admitted,null);assert.equal(result.mode,null);assert.equal(result.applied,false);assert.ok(result.issues.length);}
});
test('failed refresh masks earlier successful counts, health and modes without discarding the saved command',()=>{
  const result=roomTelemetry(fixture(),'sale',{now,unavailable:true});assert.equal(result.waiting,null);assert.equal(result.originHealthy,null);assert.equal(result.http5xx,null);assert.equal(result.mode,null);assert.equal(result.runtime.mode,'AUTO');assert.equal(result.applied,false);
});
test('origin false and recovery are actionable failures, while old Gateway counters remain unknown',()=>{
  const view=fixture();view.nodes[1].rooms[0].originHealthy=false;view.nodes[0].rooms[0].mode='RECOVERY_HOLD';delete view.nodes[1].rooms[0].http5xxLastMinute;delete view.nodes[1].rooms[0].http5xxWindowReady;
  const result=roomTelemetry(view,'sale',{now});assert.equal(result.originHealthy,false);assert.equal(result.mode,'RECOVERY_HOLD');assert.equal(result.applied,false);assert.equal(result.http5xx,null);assert.equal(result.issues.filter(issue=>issue.kind==='error').length,2);
});
test('HTTP 5xx uses fresh Gateway whole-minute observations and distinguishes warming from zero',()=>{
  const view=fixture();view.nodes[0].rooms[0].http5xxLastMinute=99;view.nodes[0].rooms[0].http5xxWindowReady=true;
  view.nodes[1].rooms[0].http5xxLastMinute=4;let result=roomTelemetry(view,'sale',{now});assert.equal(result.http5xx,4);assert.match(result.issues[0].message,/4건/);
  view.nodes[1].rooms[0].http5xxWindowReady=false;result=roomTelemetry(view,'sale',{now});assert.equal(result.http5xx,null);assert.equal(result.http5xxCollecting,true);
  delete view.nodes[1].rooms[0].http5xxWindowReady;result=roomTelemetry(view,'sale',{now});assert.equal(result.http5xx,null);assert.equal(result.http5xxCollecting,true);
  view.nodes[1].fresh=false;result=roomTelemetry(view,'sale',{now});assert.equal(result.http5xx,null);assert.equal(result.http5xxCollecting,false);
});
test('aggregate is unknown instead of a misleading partial total if any Room lacks fresh source data',()=>{
  const view=fixture();assert.equal(aggregateTelemetry(view,{now}).admitted,5);assert.equal(aggregateTelemetry(view,{now}).http5xx,0);
  view.config.rooms.push({id:'unknown',name:'미관측'});const result=aggregateTelemetry(view,{now});assert.equal(result.waiting,null);assert.equal(result.rate,null);assert.equal(result.http5xx,null);assert.equal(result.observedRooms,1);assert.equal(result.unknownOrigins,1);
  assert.equal(aggregateTelemetry(null,{now}).waiting,null);
});
test('a late polling response cannot overwrite a newer command generation',()=>{
  const current=fixture(),old={...current,generation:8};assert.equal(preferNewerDelivery(current,old),current);const next={...current,generation:10};assert.equal(preferNewerDelivery(current,next),next);
});
