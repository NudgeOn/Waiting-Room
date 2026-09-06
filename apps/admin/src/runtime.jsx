// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';

const modes={OFF:'OFF · 보호 해제',HOLD:'HOLD · 입장 일시정지',AUTO:'AUTO · 순서대로 입장',DRAINING:'DRAINING · 안전 종료 중',RECOVERY_HOLD:'장애 복구 대기'};
export default function RuntimeDashboard({csrf,session,draft,onBack}){
 const [view,setView]=useState(null),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[selected,setSelected]=useState(''),[events,setEvents]=useState([]);
 const heading=useRef(null),lock=useRef(false),pending=useRef(null),mounted=useRef(true);
 const can=action=>Boolean(csrf)&&session.capabilities.some(c=>c.action===action&&!c.requiresReauthentication);
 useEffect(()=>{mounted.current=true;let live=true,timer;
  async function refresh(){try{const result=await controlAPI('/config/delivery');if(live)setView(result.data);}catch(e){if(live)setError(e.message);}if(live)timer=setTimeout(refresh,3000);}
  heading.current?.focus();refresh();return()=>{live=false;mounted.current=false;clearTimeout(timer);};
 },[]);
 async function command(path,method,body,etag){
  if(lock.current)return;lock.current=true;setBusy(true);setError('');setNotice('');
  const signature=JSON.stringify({path,method,body,etag});if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};
  try{await controlAPI(path,{method,body,etag,key:pending.current.key,csrf});pending.current=null;const result=await controlAPI('/config/delivery');if(mounted.current){setView(result.data);setNotice('명령을 저장했습니다. 두 서비스의 적용 확인 상태를 확인하세요.');}if(selected){const out=await controlAPI(`/rooms/${selected}/events`);if(mounted.current)setEvents(out.data.items);}}
  catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}
 }
 async function choose(id){if(lock.current)return;setSelected(id);setEvents([]);try{const out=await controlAPI(`/rooms/${id}/events`);if(mounted.current)setEvents(out.data.items);}catch(e){if(mounted.current)setError(e.message);}}
 const room=view?.config.rooms.find(r=>r.id===selected),runtime=view?.runtimes.find(r=>r.roomId===selected)?.runtime;
 return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>실시간 운영</h2><p>저장된 명령과 실제 서비스의 적용 상태를 구분합니다.</p></div><button className="text-button" disabled={busy} onClick={onBack}>초안으로 돌아가기</button></div>
 {error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
 <section className="control-card"><h3>설정 배포</h3><p>저장 초안 revision {draft.data.revision} → 배포 revision {view?.revision??'확인 중'}</p><p>적용 확인: <strong>{view?.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</strong> · generation {view?.generation??'—'}</p>
 <p className="control-help">새로 활성화한 Room은 HOLD로 시작합니다. 실제 입장은 AUTO를 눌러 시작하세요. 유량 축소가 기존 입장권 수보다 작으면 적용이 대기할 수 있습니다.</p>
 {can('config.write')?<button className="primary" disabled={busy} onClick={()=>command('/config/publish','POST',{},draft.etag)}>저장 초안 배포</button>:null}
 {view?.nodes.map(n=><p key={n.id}>{n.id} · generation {n.generation} · {n.fresh?'최근 응답':'응답 오래됨'}</p>)}</section>
 <section className="control-card"><h3>운영 Room</h3>{view?.config.rooms.length===0?<p>배포된 Room이 없습니다. 초안에서 연결과 활성화 여부를 지정한 뒤 배포하세요.</p>:null}
 <ul className="room-list">{view?.config.rooms.map(r=>{const rt=view.runtimes.find(v=>v.roomId===r.id).runtime;return <li key={r.id}><button disabled={busy} onClick={()=>choose(r.id)} aria-pressed={selected===r.id}><strong>{r.name}</strong><span>{modes[rt.mode]}</span><small>revision {rt.revision} · {rt.eventState}</small></button></li>;})}</ul></section>
 {room&&runtime?<><section className="control-card"><h3>{room.name} · 유량과 모드</h3><p>{modes[runtime.mode]} · {runtime.limits.admissionsPerMinute}/분 · 활성 입장권 최대 {runtime.limits.maxActiveAdmissionLeases}</p>
 {view.nodes.map(n=>{const m=n.rooms.find(r=>r.roomId===room.id);return m?<p key={n.id}>{n.id}: {modes[m.mode]}{n.id==='coordinator'?` · WAITING ${m.waiting} · READY ${m.ready} · 입장권 ${m.leases}`:` · 원본 ${m.originHealthy?'정상':'확인 필요'} · 5분 관측 ${m.arrivalWindowReady?'완료':'수집 중'}`}</p>:null;})}
 {can('runtime.operate')?<><div className="control-actions">{[['auto','AUTO 시작'],['hold','HOLD 일시정지'],['safe-drain','안전 종료']].map(([action,label])=><button className="secondary" disabled={busy||!room.active} key={action} onClick={()=>command(`/rooms/${room.id}/runtime`,'PATCH',{action},`"runtime-${runtime.revision}"`)}>{label}</button>)}</div>
 <p className="control-help">안전 종료는 신규 대기를 막고 기존 대기자를 입장시킵니다. 대기자·READY가 0이고 원본 상태와 5분 유입 관측 조건을 만족해야 OFF가 됩니다. 수동 변경은 예약을 일시정지합니다.</p>
 <form key={room.id} onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);command(`/rooms/${room.id}/runtime`,'PATCH',{action:'set-limits',limits:{admissionsPerMinute:Number(f.get('rate')),maxActiveAdmissionLeases:Number(f.get('leases')),admissionTtlSeconds:Number(f.get('ttl'))}},`"runtime-${runtime.revision}"`);}}><fieldset disabled={busy||!room.active}><legend>실제 유량 변경</legend><div className="control-grid">{[['rate','운영 분당 입장',runtime.limits.admissionsPerMinute,view.config.profile==='high-scale-100k'?60000:6000,1],['leases','운영 최대 입장권',runtime.limits.maxActiveAdmissionLeases,view.config.profile==='high-scale-100k'?100000:10000,1],['ttl','운영 입장권 TTL (초)',runtime.limits.admissionTtlSeconds,3600,60]].map(([name,label,value,max,min])=><label className="control-field" key={name}><span>{label}</span><input name={name} type="number" required min={min} max={max} defaultValue={value}/></label>)}</div><button className="primary">유량 적용</button></fieldset></form></>:null}</section>
 <section className="control-card"><h3>예약</h3><p className="control-help">입력 시간대: {Intl.DateTimeFormat().resolvedOptions().timeZone}. 서버에는 UTC로 저장합니다. 겹치는 예약은 거부됩니다.</p>
 {can('events.write')?<form onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);const body=Object.fromEntries(['prequeueAt','admitAt','drainAt'].map(k=>[k,new Date(String(f.get(k))).toISOString()]));command(`/rooms/${room.id}/events`,'POST',body,`"runtime-${runtime.revision}"`);}}><fieldset disabled={busy||!room.active}><legend>새 예약</legend><div className="control-grid">{[['prequeueAt','사전 대기 시작'],['admitAt','입장 시작'],['drainAt','안전 종료 시작']].map(([name,label])=><label className="control-field" key={name}><span>{label}</span><input type="datetime-local" name={name} required/></label>)}</div><button className="secondary">예약 저장</button></fieldset></form>:null}
 <ul className="audit-list">{events.map(event=><li key={event.id}><strong>{event.state}</strong><span>대기 {new Date(event.prequeueAt).toLocaleString()} → 입장 {new Date(event.admitAt).toLocaleString()} → 종료 {new Date(event.drainAt).toLocaleString()}</span>{can('events.write')&&!['cancelled','completed'].includes(event.state)?<div className="control-actions">{event.state==='paused_by_override'?<button disabled={busy} onClick={()=>command(`/events/${event.id}/resume`,'POST',{},`"runtime-${runtime.revision}"`)}>예약 재개</button>:null}<button disabled={busy} onClick={()=>command(`/events/${event.id}`,'DELETE',{},`"runtime-${runtime.revision}"`)}>예약 취소</button></div>:null}</li>)}</ul></section></>:null}
 </div>;
}
