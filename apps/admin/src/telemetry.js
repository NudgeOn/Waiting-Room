// SPDX-License-Identifier: Apache-2.0
export const runtimeModes={OFF:'대기 없이 접속',HOLD:'입장 일시정지 중',AUTO:'입장 진행 중',DRAINING:'남은 사람 입장 후 종료 중',RECOVERY_HOLD:'복구 중 · 입장 대기'};
const count=value=>Number.isSafeInteger(value)&&value>=0;

export function nodeObservation(view,nodeId,roomId,{unavailable=false,now=Date.now()}={}){
  const node=view?.nodes?.find(item=>item.id===nodeId),metrics=node?.rooms?.find(item=>item.roomId===roomId),runtime=view?.runtimes?.find(item=>item.roomId===roomId)?.runtime;
  let reason='';
  if(unavailable)reason='최신 조회 실패';
  else if(!node)reason='서비스 응답 없음';
  else if(!node.fresh||!Number.isFinite(Date.parse(node.observedAt))||now-Date.parse(node.observedAt)>15000||Date.parse(node.observedAt)>now+1000)reason='응답 오래됨';
  else if(node.generation!==view.generation)reason='최신 설정 적용 대기';
  else if(!metrics||!runtime)reason='대기열 응답 없음';
  else if(metrics.revision!==runtime.revision||metrics.epoch!==runtime.epoch)reason='대기열 변경 적용 대기';
  return {node,metrics,usable:!reason,reason};
}

export function roomTelemetry(view,roomId,options={}){
  const coordinator=nodeObservation(view,'coordinator',roomId,options),gateway=nodeObservation(view,'gateway',roomId,options),runtime=view?.runtimes?.find(item=>item.roomId===roomId)?.runtime;
  const q=coordinator.usable?coordinator.metrics:null,g=gateway.usable?gateway.metrics:null;
  // READY owns a capacity lease. Claim moves READY to ADMITTED atomically;
  // subtract reservations to show admitted leases without counting them twice.
  const waiting=q&&count(q.waiting)?q.waiting:null,ready=q&&count(q.ready)?q.ready:null,leases=q&&count(q.leases)?q.leases:null,admitted=leases!==null&&ready!==null&&leases>=ready?leases-ready:null,rate=q&&count(q.rate)?q.rate:null;
  const originHealthy=typeof g?.originHealthy==='boolean'?g.originHealthy:null;
  const http5xx=g?.http5xxWindowReady===true&&count(g.http5xxLastMinute)?g.http5xxLastMinute:null;
  const issues=[];
  if(!coordinator.usable)issues.push({kind:'unknown',message:`대기 순서 처리 · ${coordinator.reason}`});
  if(!gateway.usable)issues.push({kind:'unknown',message:`방문자 접속 처리 · ${gateway.reason}`});
  if(originHealthy===false)issues.push({kind:'error',message:'실제 서비스의 응답을 확인하지 못했습니다.'});
  if(http5xx>0)issues.push({kind:'error',message:`서버 오류 · 최근 1분 ${http5xx.toLocaleString('ko-KR')}건`});
  if(q?.mode==='RECOVERY_HOLD')issues.push({kind:'error',message:'공유 상태 확인 필요 · 복구 안전 대기'});
  return {coordinator,gateway,runtime,waiting,ready,leases,admitted,rate,originHealthy,http5xx,http5xxCollecting:Boolean(g&&count(g.http5xxLastMinute)&&g.http5xxWindowReady!==true),mode:q?.mode??null,gatewayMode:g?.mode??null,applied:coordinator.usable&&gateway.usable&&q?.mode===runtime?.mode&&g?.mode===runtime?.mode,issues,arrivals:g?.arrivalWindowReady&&count(g.arrivalsFiveMinutes)?g.arrivalsFiveMinutes:null};
}

export function aggregateTelemetry(view,options={}){
  const rooms=(view?.config?.rooms??[]).map(room=>({...room,telemetry:roomTelemetry(view,room.id,options)}));
  const sum=field=>rooms.length&&rooms.every(room=>room.telemetry[field]!==null)?rooms.reduce((total,room)=>total+room.telemetry[field],0):null;
  return {rooms,waiting:sum('waiting'),ready:sum('ready'),admitted:sum('admitted'),rate:sum('rate'),http5xx:sum('http5xx'),http5xxCollecting:rooms.some(room=>room.telemetry.http5xxCollecting),healthyOrigins:rooms.filter(room=>room.telemetry.originHealthy===true).length,failedOrigins:rooms.filter(room=>room.telemetry.originHealthy===false).length,unknownOrigins:rooms.filter(room=>room.telemetry.originHealthy===null).length,issues:rooms.flatMap(room=>room.telemetry.issues.map(issue=>({...issue,roomId:room.id,roomName:room.name}))),observedRooms:rooms.filter(room=>room.telemetry.coordinator.usable&&room.telemetry.gateway.usable).length};
}

export function preferNewerDelivery(previous,next){return previous&&previous.generation>next.generation?previous:next;}
