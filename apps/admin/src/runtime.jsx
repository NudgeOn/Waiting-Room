// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {prepareAction} from './security-api.js';
import ReauthDialog from './reauth.jsx';
import EventSchedule from './events.jsx';
import RouteCheck from './route-check.jsx';
import {Link} from './navigation.jsx';
import {navigate} from './routes.js';

function LimitsForm({room,runtime,profile,busy,onCommand}){
 const [baseline,setBaseline]=useState(runtime);
 return <form onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);onCommand(`/rooms/${room.id}/runtime`,'PATCH',{action:'set-limits',limits:{admissionsPerMinute:Number(f.get('rate')),maxActiveAdmissionLeases:Number(f.get('leases')),admissionTtlSeconds:Number(f.get('ttl'))}},`"runtime-${baseline.revision}"`);}}><fieldset disabled={busy||!room.active}><legend>실제 유량 변경 · 편집 기준 revision {baseline.revision}</legend>{baseline.revision!==runtime.revision?<p className="control-warning">서버 revision이 {runtime.revision}으로 변경됐습니다. 편집값을 버리고 최신 값을 불러오거나, 기존 기준으로 제출해 충돌 여부를 확인하세요.</p>:null}<button type="button" className="text-button" onClick={()=>setBaseline(runtime)}>최신 유량 불러오기</button><div className="control-grid" key={baseline.revision}>{[['rate','운영 분당 입장',baseline.limits.admissionsPerMinute,profile==='high-scale-100k'?60000:6000,1],['leases','운영 최대 입장권',baseline.limits.maxActiveAdmissionLeases,profile==='high-scale-100k'?100000:10000,1],['ttl','운영 입장권 TTL (초)',baseline.limits.admissionTtlSeconds,3600,60]].map(([name,label,value,max,min])=><label className="control-field" key={name}><span>{label}</span><input name={name} type="number" required min={min} max={max} defaultValue={value}/></label>)}</div><button className="primary">유량 적용</button></fieldset></form>;
}

const recoveryReasons={primary_changed:'Valkey 서버가 변경됨',uncertain_write:'쓰기 결과를 확인할 수 없음',state_uncertain:'공유 상태 불일치',clock_rollback:'서버 시간이 뒤로 이동함',initialization_uncertainty:'시작 시 상태 확인 필요'};
const modes={OFF:'OFF · 보호 해제',HOLD:'HOLD · 입장 일시정지',AUTO:'AUTO · 순서대로 입장',DRAINING:'DRAINING · 안전 종료 중',RECOVERY_HOLD:'장애 복구 대기'};
export default function RuntimeDashboard({csrf,session,draft,onBack,roomId,tab}){
 const [view,setView]=useState(null),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[events,setEvents]=useState([]);
 const selected=roomId??'';
 const heading=useRef(null),lock=useRef(false),pending=useRef(null),mounted=useRef(true),eventSequence=useRef(0);
 const [sensitive,setSensitive]=useState(null),[totpEnabled,setTOTPEnabled]=useState(true);
 const can=action=>Boolean(csrf)&&session.capabilities.some(c=>c.action===action&&!c.requiresReauthentication);
 useEffect(()=>{mounted.current=true;let live=true,timer;
  async function refresh(){try{const result=await controlAPI('/config/delivery');if(live)setView(result.data);}catch(e){if(live)setError(e.message);}if(live)timer=setTimeout(refresh,3000);}
  heading.current?.focus();refresh();return()=>{live=false;mounted.current=false;clearTimeout(timer);};
 },[]);
 useEffect(()=>{if(!selected)return;let live=true;const sequence=++eventSequence.current;
  controlAPI(`/rooms/${selected}/events`).then(out=>{if(live&&sequence===eventSequence.current)setEvents(out.data.items);}).catch(e=>{if(live&&sequence===eventSequence.current)setError(e.message);});
  return()=>{live=false;};
 },[selected,view?.generation]);
 async function command(path,method,body,etag){
  if(lock.current)return;lock.current=true;setBusy(true);setError('');setNotice('');
  const signature=JSON.stringify({path,method,body,etag});if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};
  try{await controlAPI(path,{method,body,etag,key:pending.current.key,csrf});pending.current=null;const result=await controlAPI('/config/delivery');if(mounted.current){setView(result.data);setNotice('명령을 저장했습니다. 두 서비스의 적용 확인 상태를 확인하세요.');}return true;}
  catch(e){if(mounted.current)setError(e.message);return false;}finally{lock.current=false;if(mounted.current)setBusy(false);}
 }
 function choose(id){if(!lock.current)navigate(`/rooms/${id}/operations`);}
 async function instantOff(room,runtime){if(lock.current)return;lock.current=true;setBusy(true);try{const policy=await controlAPI('/security/totp');if(mounted.current){setTOTPEnabled(policy.data.enabled);setSensitive(prepareAction({path:`/rooms/${room.id}/runtime`,method:'PATCH',target:room.id,action:'runtime.instant_off',body:{action:'instant-off'},etag:`"runtime-${runtime.revision}"`,label:`${room.name} 보호 즉시 해제. 대기 중인 사용자도 원본으로 접근할 수 있습니다.`}));}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
 const room=view?.config.rooms.find(r=>r.id===selected),runtime=view?.runtimes.find(r=>r.roomId===selected)?.runtime;
 return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>{tab==='schedule'?'Room 일정':tab==='verification'?'Room 검증':'실시간 운영'}</h2><p>저장된 명령과 실제 서비스의 적용 상태를 구분합니다.</p></div><button className="text-button" disabled={busy} onClick={onBack}>초안으로 돌아가기</button></div>
 {error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
 {tab==='verification'?<RouteCheck csrf={csrf} canRead={session.capabilities.some(c=>c.action==='config.read')} roomId={roomId} draftRevision={draft.data.revision} publishedRevision={view?.revision} generation={view?.generation}/>:null}
 {!roomId?<section className="control-card"><h3>설정 배포</h3><p>저장 초안 revision {draft.data.revision} → 배포 revision {view?.revision??'확인 중'}</p><p>적용 확인: <strong>{view?.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</strong> · generation {view?.generation??'—'}</p>
 <p className="control-help">새로 활성화한 Room은 HOLD로 시작합니다. 실제 입장은 AUTO를 눌러 시작하세요. 유량 축소가 기존 입장권 수보다 작으면 적용이 대기할 수 있습니다.</p>
 {can('config.write')?<button className="primary" disabled={busy} onClick={()=>command('/config/publish','POST',{},draft.etag)}>저장 초안 배포</button>:null}
 {view?.nodes.map(n=><p key={n.id}>{n.id} · generation {n.generation} · {n.fresh?'최근 응답':'응답 오래됨'}</p>)}</section>:null}
 <section className="control-card"><h3>운영 Room</h3>{view?.config.rooms.length===0?<p>배포된 Room이 없습니다. 초안에서 연결과 활성화 여부를 지정한 뒤 배포하세요.</p>:null}
 <ul className="room-list">{view?.config.rooms.map(r=>{const rt=view.runtimes.find(v=>v.roomId===r.id).runtime;return <li key={r.id}><button disabled={busy} onClick={()=>choose(r.id)} aria-pressed={selected===r.id}><strong>{r.name}</strong><span>{modes[rt.mode]}</span><small>revision {rt.revision} · {rt.eventState}</small></button></li>;})}</ul></section>
 {roomId&&view&&!room?<section className="control-card"><h3>배포된 Room을 찾을 수 없습니다.</h3><p>저장 초안을 배포한 후 운영·일정·연결 상태를 확인할 수 있습니다.</p><Link to="/dashboard/runtime">설정 배포로 이동</Link></section>:null}
 {room&&runtime?<>{tab==='verification'?<section className="control-card"><h3>서비스 적용과 연결 확인</h3><p>배포 revision {view.revision} · generation {view.generation} · {view.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</p>{view.nodes.map(n=>{const m=n.rooms.find(r=>r.roomId===room.id);return <p key={n.id}>{n.id}: {n.fresh?'최근 응답':'응답 오래됨'} · generation {n.generation}{m&&n.id==='gateway'?` · 원본 ${m.originHealthy?'정상':'확인 필요'}`:''}</p>;})}<p className="control-warning">상태 조회는 부하 시험 통과를 의미하지 않습니다. Quick 20·Smoke 1K의 화면 실행은 아직 미지원입니다. 고객 원본에 자동으로 시험 트래픽을 보내지 않습니다.</p><Link to="/dashboard/runtime">전체 배포 상태 확인</Link></section>:null}
 {tab==='operations'?<section className="control-card"><h3>{room.name} · 유량과 모드</h3><p>{modes[runtime.mode]} · {runtime.limits.admissionsPerMinute}/분 · 활성 입장권 최대 {runtime.limits.maxActiveAdmissionLeases}</p>
 {view.nodes.map(n=>{const m=n.rooms.find(r=>r.roomId===room.id);return m?<p key={n.id}>{n.id}: {modes[m.mode]}{n.id==='coordinator'?` · WAITING ${m.waiting} · READY ${m.ready} · 입장권 ${m.leases}`:` · 원본 ${m.originHealthy?'정상':'확인 필요'} · 5분 관측 ${m.arrivalWindowReady?'완료':'수집 중'}`}</p>:null;})}
 {view.nodes.filter(n=>n.id==='coordinator').flatMap(n=>n.rooms.filter(m=>m.roomId===room.id&&m.mode==='RECOVERY_HOLD')).map(m=><aside key={m.roomId} className="control-warning" role="status"><strong>입장 중지 · 복구 안전 대기</strong><p>{recoveryReasons[m.recoveryReason]||'공유 상태를 확인 중입니다.'} · fencing {m.recoveryFence||'확인 중'}</p><p>{m.recoveryUntil?'검증 시작 가능 시각: '+new Date(m.recoveryUntil).toLocaleString():'아직 안전 대기 종료 시각을 확인하지 못했습니다.'}</p><p>{m.recoveryValidation?'저장 상태 검증이 실패했습니다. 추정 복구나 자동 입장 재개를 하지 않습니다.':'이 시각이 지나도 대기표·입장 예약·인덱스 검증을 통과해야 재개합니다. 기존 입장권의 만료 시각은 늘리지 않습니다.'}</p></aside>)}
 {can('runtime.operate')?<><div className="control-actions">{[['auto','AUTO 시작'],['hold','HOLD 일시정지'],['safe-drain','안전 종료']].map(([action,label])=><button className="secondary" disabled={busy||!room.active} key={action} onClick={()=>command(`/rooms/${room.id}/runtime`,'PATCH',{action},`"runtime-${runtime.revision}"`)}>{label}</button>)}</div>
 <p className="control-help">안전 종료는 신규 대기를 막고 기존 대기자를 입장시킵니다. 대기자·READY가 0이고 원본 상태와 5분 유입 관측 조건을 만족해야 OFF가 됩니다. 수동 변경은 예약을 일시정지합니다.</p>
 <LimitsForm key={room.id} room={room} runtime={runtime} profile={view.config.profile} busy={busy} onCommand={command}/></>:null}{session.capabilities.some(c=>c.action==='runtime.instant_off')?<button className="secondary" disabled={busy||!room.active} onClick={()=>instantOff(room,runtime)}>재인증 후 즉시 OFF</button>:null}</section>:null}
 {tab==='schedule'?<EventSchedule key={room.id} room={room} runtime={runtime} events={events} busy={busy} canWrite={can('events.write')} onCommand={command}/>:null}</>:null}
 {sensitive?<ReauthDialog key={sensitive.key} request={sensitive} csrf={csrf} requireTOTP={totpEnabled} onCancel={()=>setSensitive(null)} onComplete={async()=>{setSensitive(null);try{const out=await controlAPI('/config/delivery');if(mounted.current){setView(out.data);setNotice('즉시 OFF 명령을 저장했습니다. 서비스 적용 상태를 확인하세요.');}}catch(e){if(mounted.current)setError(e.message);}}}/>:null}
 </div>;
}
