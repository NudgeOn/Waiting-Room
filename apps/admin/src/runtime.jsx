// SPDX-License-Identifier: Apache-2.0
import React,{lazy,Suspense,useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {prepareAction} from './security-api.js';
import ReauthDialog from './reauth.jsx';
import EventSchedule from './events.jsx';
import RouteCheck from './route-check.jsx';
import {Link} from './navigation.jsx';
import {navigate} from './routes.js';
import {roomTelemetry,preferNewerDelivery} from './telemetry.js';
import {QueueMetrics,RoomHealth,RuntimeState,useObservationClock} from './telemetry.jsx';
const TrafficLab=lazy(()=>import('./traffic-lab.jsx'));

function LimitsForm({room,runtime,profile,busy,onCommand}){
 const [baseline,setBaseline]=useState(runtime);
 return <form onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);onCommand(`/rooms/${room.id}/runtime`,'PATCH',{action:'set-limits',limits:{admissionsPerMinute:Number(f.get('rate')),maxActiveAdmissionLeases:Number(f.get('leases')),admissionTtlSeconds:Number(f.get('ttl'))}},`"runtime-${baseline.revision}"`);}}><fieldset disabled={busy||!room.active}><legend>실제 유량 변경 · 편집 기준 revision {baseline.revision}</legend>{baseline.revision!==runtime.revision?<p className="control-warning">서버 revision이 {runtime.revision}으로 변경됐습니다. 편집값을 버리고 최신 값을 불러오거나, 기존 기준으로 제출해 충돌 여부를 확인하세요.</p>:null}<button type="button" className="text-button" onClick={()=>setBaseline(runtime)}>최신 유량 불러오기</button><div className="control-grid" key={baseline.revision}>{[['rate','운영 분당 입장',baseline.limits.admissionsPerMinute,profile==='high-scale-100k'?60000:6000,1],['leases','운영 최대 입장권',baseline.limits.maxActiveAdmissionLeases,profile==='high-scale-100k'?100000:10000,1],['ttl','운영 입장권 TTL (초)',baseline.limits.admissionTtlSeconds,3600,60]].map(([name,label,value,max,min])=><label className="control-field" key={name}><span>{label}</span><input name={name} type="number" required min={min} max={max} defaultValue={value}/></label>)}</div><button className="primary">유량 적용</button></fieldset></form>;
}

const recoveryReasons={primary_changed:'Valkey 서버가 변경됨',uncertain_write:'쓰기 결과를 확인할 수 없음',state_uncertain:'공유 상태 불일치',clock_rollback:'서버 시간이 뒤로 이동함',initialization_uncertainty:'시작 시 상태 확인 필요',schema_migration:'기존 대기열 이행 검증',epoch_reset:'새 epoch의 이전 입장권 만료 대기'};
const modes={OFF:'OFF · 보호 해제',HOLD:'HOLD · 입장 일시정지',AUTO:'AUTO · 순서대로 입장',DRAINING:'DRAINING · 안전 종료 중',RECOVERY_HOLD:'장애 복구 대기'};
export default function RuntimeDashboard({csrf,session,draft,onBack,roomId,tab}){
 const [view,setView]=useState(null),[error,setError]=useState(''),[refreshError,setRefreshError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[events,setEvents]=useState([]),[updatedAt,setUpdatedAt]=useState(null),[refreshKey,setRefreshKey]=useState(0);
 const now=useObservationClock();
 const selected=roomId??'';
 const heading=useRef(null),lock=useRef(false),pending=useRef(null),mounted=useRef(true),eventSequence=useRef(0);
 const [sensitive,setSensitive]=useState(null),[totpEnabled,setTOTPEnabled]=useState(true);
 const can=action=>Boolean(csrf)&&session.capabilities.some(c=>c.action===action&&!c.requiresReauthentication);
 useEffect(()=>{mounted.current=true;let live=true,timer;
  async function refresh(){try{const result=await controlAPI('/config/delivery');if(live){setView(previous=>preferNewerDelivery(previous,result.data));setUpdatedAt(Date.now());setRefreshError('');}}catch(e){if(live)setRefreshError(e.message);}if(live)timer=setTimeout(refresh,3000);}
  refresh();return()=>{live=false;mounted.current=false;clearTimeout(timer);};
 },[refreshKey]);
 useEffect(()=>{heading.current?.focus();},[]);
 useEffect(()=>{if(!selected)return;let live=true;const sequence=++eventSequence.current;
  controlAPI(`/rooms/${selected}/events`).then(out=>{if(live&&sequence===eventSequence.current)setEvents(out.data.items);}).catch(e=>{if(live&&sequence===eventSequence.current)setError(e.message);});
  return()=>{live=false;};
 },[selected,view?.generation]);
 async function command(path,method,body,etag){
  if(lock.current)return;lock.current=true;setBusy(true);setError('');setNotice('');
  const signature=JSON.stringify({path,method,body,etag});if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};
  try{await controlAPI(path,{method,body,etag,key:pending.current.key,csrf});pending.current=null;if(mounted.current)setNotice('명령을 저장했습니다. 두 서비스의 적용 확인 상태를 확인하세요.');try{const result=await controlAPI('/config/delivery');if(mounted.current){setView(previous=>preferNewerDelivery(previous,result.data));setUpdatedAt(Date.now());setRefreshError('');}}catch(e){if(mounted.current)setRefreshError(e.message);}return true;}
  catch(e){if(mounted.current)setError(e.message);return false;}finally{lock.current=false;if(mounted.current)setBusy(false);}
 }
 function choose(id){if(!lock.current)navigate(`/rooms/${id}/operations`);}
 async function instantOff(room,runtime){if(lock.current)return;lock.current=true;setBusy(true);try{const policy=await controlAPI('/security/totp');if(mounted.current){setTOTPEnabled(policy.data.enabled);setSensitive(prepareAction({path:`/rooms/${room.id}/runtime`,method:'PATCH',target:room.id,action:'runtime.instant_off',body:{action:'instant-off'},etag:`"runtime-${runtime.revision}"`,label:`${room.name} 보호 즉시 해제. 대기 중인 사용자도 원본으로 접근할 수 있습니다.`}));}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
 async function newEpoch(room,runtime){if(lock.current)return;lock.current=true;setBusy(true);try{const policy=await controlAPI('/security/totp');if(mounted.current){setTOTPEnabled(policy.data.enabled);const affected=view.config.rooms.length;const observed=view.nodes.find(n=>n.id==='coordinator'&&n.fresh&&n.generation===view.generation);const count=observed?.rooms.length===affected?observed.rooms.reduce((n,r)=>n+r.waiting+r.leases,0):null;setSensitive(prepareAction({path:`/rooms/${room.id}/runtime`,method:'PATCH',target:room.id,action:'runtime.new_epoch',body:{action:'new-epoch',scope:'installation',generation:view.generation},etag:`"runtime-${runtime.revision}"`,label:`설치 전체 ${affected}개 Room을 epoch ${runtime.epoch+1}로 복구합니다. 기존 대기표와 입장권은 새 epoch에서 사용할 수 없습니다. 관측된 영향: ${count===null?'미확인':count+'개 (최근 관측값, 변동 가능)'}. 모든 예약을 일시정지하고 활성 Room을 HOLD로 전환합니다. 이전 입장권 만료를 위해 실제 적용부터 최소 60분 30초 동안 입장을 차단합니다.`}));}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}

 const room=view?.config.rooms.find(r=>r.id===selected),runtime=view?.runtimes.find(r=>r.roomId===selected)?.runtime;
 const telemetry=roomTelemetry(view,selected,{unavailable:Boolean(refreshError),now}),unobserved=!telemetry.coordinator.usable||!telemetry.gateway.usable;
 return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>{tab==='schedule'?'Room 일정':tab==='verification'?'Room 검증':'실시간 운영'}</h2><p>저장된 명령과 실제 서비스의 적용 상태를 구분합니다.</p></div><button className="text-button" disabled={busy} onClick={onBack}>초안으로 돌아가기</button></div>
 {error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
 {refreshError?<p className="error" role="alert">{refreshError} · 최신 관측값이 없어 지표와 모드 변경을 잠시 사용할 수 없습니다.</p>:null}
 <div className="ops-live-line"><span><i aria-hidden="true"/>3초마다 자동 갱신</span>{updatedAt?<span>마지막 조회 {new Date(updatedAt).toLocaleTimeString('ko-KR')}</span>:null}<button type="button" className="ops-refresh" disabled={busy} onClick={()=>setRefreshKey(value=>value+1)}>운영 상태 새로고침</button></div>
 {!view?<p role="status">{refreshError?'운영 상태를 표시할 수 없습니다.':'운영 상태 불러오는 중…'}</p>:null}
 {tab==='verification'?<RouteCheck csrf={csrf} canRead={session.capabilities.some(c=>c.action==='config.read')} roomId={roomId} draftRevision={draft.data.revision} publishedRevision={view?.revision} generation={view?.generation}/>:null}
 {tab==='verification'?<Suspense fallback={<p role="status">Traffic Lab을 불러오는 중…</p>}><TrafficLab csrf={csrf} session={session} embedded/></Suspense>:null}
 {!roomId?<section className="control-card"><h3>설정 배포</h3><p>저장 초안 revision {draft.data.revision} → 배포 revision {view?.revision??'확인 중'}</p><p>적용 확인: <strong>{view?.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</strong> · generation {view?.generation??'—'}</p>
 <p className="control-help">새로 활성화한 Room은 HOLD로 시작합니다. 실제 입장은 AUTO를 눌러 시작하세요. 유량 축소가 기존 입장권 수보다 작으면 적용이 대기할 수 있습니다.</p>
 {can('config.write')?<button className="primary" disabled={busy} onClick={()=>command('/config/publish','POST',{},draft.etag)}>저장 초안 배포</button>:null}
 {view?.nodes.map(n=><p key={n.id}>{n.id} · generation {n.generation} · {n.fresh?'최근 응답':'응답 오래됨'}</p>)}</section>:null}
 <section className="control-card"><h3>운영 Room</h3>{view?.config.rooms.length===0?<p>배포된 Room이 없습니다. 초안에서 연결과 활성화 여부를 지정한 뒤 배포하세요.</p>:null}
 <ul className="room-list">{view?.config.rooms.map(r=>{const rt=view.runtimes.find(v=>v.roomId===r.id).runtime;return <li key={r.id}><button disabled={busy} onClick={()=>choose(r.id)} aria-pressed={selected===r.id}><strong>{r.name}</strong><span>{modes[rt.mode]}</span><small>revision {rt.revision} · {rt.eventState}</small></button></li>;})}</ul></section>
 {roomId&&view&&!room?<section className="control-card"><h3>배포된 Room을 찾을 수 없습니다.</h3><p>저장 초안을 배포한 후 운영·일정·연결 상태를 확인할 수 있습니다.</p><Link to="/dashboard/runtime">설정 배포로 이동</Link></section>:null}
 {room&&runtime?<>{tab==='verification'?<section className="control-card"><h3>서비스 적용과 연결 확인</h3><p>배포 revision {view.revision} · generation {view.generation} · {view.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</p>{view.nodes.map(n=>{const m=n.rooms.find(r=>r.roomId===room.id);return <p key={n.id}>{n.id}: {n.fresh?'최근 응답':'응답 오래됨'} · generation {n.generation}{m&&n.id==='gateway'?` · 원본 ${m.originHealthy?'정상':'확인 필요'}`:''}</p>;})}<p className="control-warning">이 상태는 현재 Room의 배포 관측입니다. 위 Traffic Lab은 별도 샘플 환경의 기능 시험이며, 현재 Room이나 고객 원본의 성능 검증을 대신하지 않습니다.</p><Link to="/dashboard/runtime">전체 배포 상태 확인</Link></section>:null}
 {tab==='operations'?<section className="control-card"><h3>{room.name} · 유량과 모드</h3><p className="ops-description">{room.hostname} · 활성 입장권 최대 {runtime.limits.maxActiveAdmissionLeases.toLocaleString('ko-KR')}개 · 새 입장권 유효시간 {runtime.limits.admissionTtlSeconds.toLocaleString('ko-KR')}초</p><RoomHealth data={telemetry}/><RuntimeState data={telemetry}/><QueueMetrics data={telemetry}/>
 <p className="ops-metrics-note"><strong>창을 닫아도 입장권은 즉시 반환되지 않습니다.</strong> 최대 입장권이 모두 사용 중이면 기존 입장권의 만료와 검증 유예가 끝난 뒤 다음 순서를 배정합니다. 유효시간 변경은 새로 발급하는 입장권부터 적용됩니다. 유효 입장권은 READY 예약을 제외한 값이며 현재 접속자 수나 누적 방문자 수가 아닙니다. 실제 입장 배정은 최근 60초의 READY 예약 발급 수입니다.</p>
 {telemetry.issues.length?<aside className="ops-issues"><h3>확인이 필요한 상태</h3><ul>{telemetry.issues.map((issue,index)=><li key={index}>{issue.message}</li>)}</ul></aside>:null}
 <p className="ops-last-observation">Coordinator · {telemetry.coordinator.node?`${new Date(telemetry.coordinator.node.observedAt).toLocaleTimeString('ko-KR')} 관측`:'응답 없음'} · Gateway · {telemetry.gateway.node?`${new Date(telemetry.gateway.node.observedAt).toLocaleTimeString('ko-KR')} 관측`:'응답 없음'}<br/>최근 5분 유입 {telemetry.arrivals===null?'관측 창 수집 중 / 미확인':`${telemetry.arrivals.toLocaleString('ko-KR')}건`} · HTTP 5xx는 Gateway가 관측한 Room 요청의 최종 500~599 응답 수입니다.</p>
 {view.nodes.filter(n=>n.id==='coordinator').flatMap(n=>n.rooms.filter(m=>m.roomId===room.id&&m.mode==='RECOVERY_HOLD')).map(m=><aside key={m.roomId} className="control-warning" role="status"><strong>입장 중지 · 복구 안전 대기</strong><p>{recoveryReasons[m.recoveryReason]||'공유 상태를 확인 중입니다.'} · fencing {m.recoveryFence||'확인 중'}</p><p>{m.recoveryUntil?'검증 시작 가능 시각: '+new Date(m.recoveryUntil).toLocaleString():'아직 안전 대기 종료 시각을 확인하지 못했습니다.'}</p><p>{m.recoveryValidation?'저장 상태 검증이 실패했습니다. 추정 복구나 자동 입장 재개를 하지 않습니다.':'이 시각이 지나도 대기표·입장 예약·인덱스 검증을 통과해야 재개합니다. 기존 입장권의 만료 시각은 늘리지 않습니다.'}</p></aside>)}
 {can('runtime.operate')?<><div className="ops-mode-actions">{[['auto','AUTO 시작','설정한 유량 안에서 기다리는 순서대로 입장을 배정합니다.'],['hold','HOLD 일시정지','신규 입장 배정을 멈춥니다. 기존 입장권은 유지됩니다.'],['safe-drain','안전 종료','신규 대기를 막고 남은 대기자를 순서대로 입장시킵니다.']].map(([action,label,description])=><div key={action}><button className="secondary" disabled={busy||!room.active||unobserved} onClick={()=>command(`/rooms/${room.id}/runtime`,'PATCH',{action},`"runtime-${runtime.revision}"`)}>{label}</button><p>{description}</p></div>)}</div>
 <p className="control-help">안전 종료는 신규 대기를 막고 기존 대기자를 입장시킵니다. 대기자·READY가 0이고 원본 상태와 5분 유입 관측 조건을 만족해야 OFF가 됩니다. 수동 변경은 예약을 일시정지합니다.</p>
 <LimitsForm key={room.id} room={room} runtime={runtime} profile={view.config.profile} busy={busy||unobserved} onCommand={command}/></>:null}{session.capabilities.some(c=>c.action==='runtime.instant_off')?<button className="secondary" disabled={busy||!room.active||Boolean(refreshError)} onClick={()=>instantOff(room,runtime)}>재인증 후 즉시 OFF</button>:null}{session.capabilities.some(c=>c.action==='runtime.new_epoch')?<button className="secondary" disabled={busy||Boolean(refreshError)} onClick={()=>newEpoch(room,runtime)}>재인증 후 설치 전체 새 epoch 복구</button>:null}</section>:null}
 {tab==='schedule'?<EventSchedule key={room.id} room={room} runtime={runtime} events={events} busy={busy} canWrite={can('events.write')} onCommand={command}/>:null}</>:null}
 {sensitive?<ReauthDialog key={sensitive.key} request={sensitive} csrf={csrf} requireTOTP={totpEnabled} onCancel={()=>setSensitive(null)} onComplete={async()=>{setSensitive(null);try{const out=await controlAPI('/config/delivery');if(mounted.current){setView(out.data);setNotice('재인증한 명령을 저장했습니다. 서비스 적용 상태와 안전 대기 시각을 확인하세요.');}}catch(e){if(mounted.current)setError(e.message);}}}/>:null}
 </div>;
}
