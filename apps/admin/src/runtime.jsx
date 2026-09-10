// SPDX-License-Identifier: Apache-2.0
import React,{lazy,Suspense,useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {prepareAction} from './security-api.js';
import ReauthDialog from './reauth.jsx';
import EventSchedule from './events.jsx';
import OperatorGuide from './operator-guide.jsx';
import RouteCheck from './route-check.jsx';
import {Link} from './navigation.jsx';
import {navigate} from './routes.js';
import {roomTelemetry,preferNewerDelivery} from './telemetry.js';
import {QueueMetrics,RoomHealth,RuntimeState,useObservationClock} from './telemetry.jsx';
const TrafficLab=lazy(()=>import('./traffic-lab.jsx'));

function LimitsForm({room,runtime,profile,busy,onCommand}){
 const [baseline,setBaseline]=useState(runtime);
 return <form className="limits-form" onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);onCommand(`/rooms/${room.id}/runtime`,'PATCH',{action:'set-limits',limits:{admissionsPerMinute:Number(f.get('rate')),maxActiveAdmissionLeases:Number(f.get('leases')),admissionTtlSeconds:Number(f.get('ttl'))}},`"runtime-${baseline.revision}"`);}}><fieldset disabled={busy||!room.active}><legend>입장 인원 조절</legend>{baseline.revision!==runtime.revision?<p className="control-warning">다른 곳에서 입장 설정이 변경됐습니다. 아래 버튼으로 최신 설정을 불러온 뒤 다시 수정해 주세요.</p>:null}<button type="button" className="text-button" onClick={()=>setBaseline(runtime)}>최신 입장 설정 불러오기</button><div className="control-grid" key={baseline.revision}>{[['rate','1분당 입장 허용 인원',baseline.limits.admissionsPerMinute,profile==='high-scale-100k'?60000:6000,1],['leases','동시에 유지할 입장권 수',baseline.limits.maxActiveAdmissionLeases,profile==='high-scale-100k'?100000:10000,1],['ttl','입장권 유효 시간 (초)',baseline.limits.admissionTtlSeconds,3600,60]].map(([name,label,value,max,min])=><label className="control-field" key={name}><span>{label}</span><input name={name} type="number" required min={min} max={max} defaultValue={value}/></label>)}</div><button className="primary">입장 설정 적용</button></fieldset></form>;
}

const recoveryReasons={primary_changed:'Valkey 서버가 변경됨',uncertain_write:'쓰기 결과를 확인할 수 없음',state_uncertain:'공유 상태 불일치',clock_rollback:'서버 시간이 뒤로 이동함',initialization_uncertainty:'시작 시 상태 확인 필요',schema_migration:'기존 대기열 이행 검증',epoch_reset:'초기화 전 입장권의 만료 대기'};
const modes={OFF:'대기 없이 접속',HOLD:'입장 일시정지 중',AUTO:'입장 진행 중',DRAINING:'남은 사람 입장 후 종료 중',RECOVERY_HOLD:'장애 복구 대기'};
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
  try{await controlAPI(path,{method,body,etag,key:pending.current.key,csrf});pending.current=null;if(mounted.current){const label={auto:'입장 시작',hold:'입장 잠시 멈춤','safe-drain':'남은 사람 입장 후 종료','set-limits':'입장 설정 변경'}[body?.action];setNotice(`${label?label+' ':''}명령을 저장했습니다. (${new Date().toLocaleTimeString('ko-KR')}) 아래 선택 표시와 서비스 적용 상태를 확인하세요.`);}try{const result=await controlAPI('/config/delivery');if(mounted.current){setView(previous=>preferNewerDelivery(previous,result.data));setUpdatedAt(Date.now());setRefreshError('');}}catch(e){if(mounted.current)setRefreshError(e.message);}return true;}
  catch(e){if(mounted.current)setError(e.message);return false;}finally{lock.current=false;if(mounted.current)setBusy(false);}
 }
 function choose(id){if(!lock.current)navigate(`/rooms/${id}/operations`);}
 async function instantOff(room,runtime){if(lock.current)return;lock.current=true;setBusy(true);try{const policy=await controlAPI('/security/totp');if(mounted.current){setTOTPEnabled(policy.data.enabled);setSensitive(prepareAction({path:`/rooms/${room.id}/runtime`,method:'PATCH',target:room.id,action:'runtime.instant_off',body:{action:'instant-off'},etag:`"runtime-${runtime.revision}"`,label:`${room.name}의 대기열 보호를 바로 끕니다. 대기 중인 사용자도 순서를 기다리지 않고 서비스에 접속할 수 있습니다.`}));}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
 async function newEpoch(room,runtime){if(lock.current)return;lock.current=true;setBusy(true);try{const policy=await controlAPI('/security/totp');if(mounted.current){setTOTPEnabled(policy.data.enabled);const affected=view.config.rooms.length;const observed=view.nodes.find(n=>n.id==='coordinator'&&n.fresh&&n.generation===view.generation);const count=observed?.rooms.length===affected?observed.rooms.reduce((n,r)=>n+r.waiting+r.leases,0):null;setSensitive(prepareAction({path:`/rooms/${room.id}/runtime`,method:'PATCH',target:room.id,action:'runtime.new_epoch',body:{action:'new-epoch',scope:'installation',generation:view.generation},etag:`"runtime-${runtime.revision}"`,label:`전체 대기열 ${affected}개를 초기화합니다. 기존 대기표와 입장권은 더 이상 사용할 수 없습니다. 최근 확인된 대기표와 입장권: ${count===null?'미확인':count+'개 (변동 가능)'}. 모든 예약과 신규 입장을 일시정지합니다. 이전 입장권이 만료될 때까지 실제 적용부터 최소 60분 30초 동안 새 입장을 막습니다.`}));}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}

 const room=view?.config.rooms.find(r=>r.id===selected),runtime=view?.runtimes.find(r=>r.roomId===selected)?.runtime;
 const telemetry=roomTelemetry(view,selected,{unavailable:Boolean(refreshError),now}),unobserved=!telemetry.coordinator.usable||!telemetry.gateway.usable;
 return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>{tab==='schedule'?'입장 예약':tab==='verification'?'연결 확인':'실시간 운영'}</h2><p>입장을 시작하거나 잠시 멈출 수 있어요. 선택한 내용이 적용됐는지 확인하세요.</p></div><button className="text-button" disabled={busy} onClick={onBack}>대기열 목록으로</button></div>
 <OperatorGuide current={roomId?3:2} roomId={roomId}/>{error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
 {refreshError?<p className="error" role="alert">{refreshError} · 최신 관측값이 없어 지표와 모드 변경을 잠시 사용할 수 없습니다.</p>:null}
 <div className="ops-live-line"><span><i aria-hidden="true"/>3초마다 자동 갱신</span>{updatedAt?<span>마지막 조회 {new Date(updatedAt).toLocaleTimeString('ko-KR')}</span>:null}<button type="button" className="ops-refresh" disabled={busy} onClick={()=>setRefreshKey(value=>value+1)}>운영 상태 새로고침</button></div>
 {!view?<p role="status">{refreshError?'운영 상태를 표시할 수 없습니다.':'운영 상태 불러오는 중…'}</p>:null}
 {tab==='verification'?<RouteCheck csrf={csrf} canRead={session.capabilities.some(c=>c.action==='config.read')} roomId={roomId} draftRevision={draft.data.revision} publishedRevision={view?.revision} generation={view?.generation}/>:null}
 {tab==='verification'?<Suspense fallback={<p role="status">Traffic Lab을 불러오는 중…</p>}><TrafficLab csrf={csrf} session={session} embedded/></Suspense>:null}
 {!roomId?<section className="control-card"><h3>저장한 설정을 서비스에 적용</h3><p>설정 저장과 실제 적용은 별도 단계입니다. 아래 버튼을 누르면 준비한 설정을 서비스에 반영합니다.</p><p>적용 상태: <strong>{!refreshError&&view?.state==='applied'?'현재 적용된 설정 확인 완료':'적용 상태 확인 중'}</strong>{view&&draft.data.revision!==view.revision?' · 아직 적용하지 않은 변경이 있습니다.':''}</p>
 <p className="control-help">처음 적용하면 입장은 잠시 멈춘 상태로 시작합니다. 아래 대기열을 선택한 뒤 ‘입장 시작’을 눌러 주세요. 허용 입장권 수를 줄이면 기존 입장권이 만료될 때까지 기다릴 수 있습니다.</p>
 {can('config.write')?<button className="primary" disabled={busy} onClick={()=>command('/config/publish','POST',{},draft.etag)}>준비한 설정 적용</button>:null}
 <details className="operator-details"><summary>기술 정보 보기</summary><p>준비 설정 revision {draft.data.revision} · 적용 revision {view?.revision??'—'} · generation {view?.generation??'—'}</p>{view?.nodes.map(n=><p key={n.id}>{n.id} · generation {n.generation} · {n.fresh?'최근 응답':'응답 오래됨'}</p>)}</details></section>:null}
 <section className="control-card"><h3>운영 중인 대기열</h3>{view?.config.rooms.length===0?<p>아직 적용된 대기열이 없습니다. 대기열 설정을 저장하고 ‘준비한 설정 적용’을 눌러 주세요.</p>:null}
 <ul className="room-list">{view?.config.rooms.map(r=>{const rt=view.runtimes.find(v=>v.roomId===r.id).runtime;return <li key={r.id}><button disabled={busy} onClick={()=>choose(r.id)} aria-pressed={selected===r.id}><strong>{r.name}</strong><span>{modes[rt.mode]}</span><small>{rt.eventState==='paused_by_override'?'예약 일시정지':rt.eventState==='none'?'설정된 예약 없음':'예약 사용 중'}</small></button></li>;})}</ul></section>
 {roomId&&view&&!room?<section className="control-card"><h3>아직 적용된 대기열을 찾을 수 없습니다.</h3><p>저장한 설정을 서비스에 적용하면 입장 운영과 예약을 사용할 수 있습니다.</p><Link to="/dashboard/runtime">설정 적용으로 이동</Link></section>:null}
 {room&&runtime?<>{tab==='verification'?<section className="control-card"><h3>서비스 적용과 연결 확인</h3><p>배포 revision {view.revision} · generation {view.generation} · {view.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}</p>{view.nodes.map(n=>{const m=n.rooms.find(r=>r.roomId===room.id);return <p key={n.id}>{n.id}: {n.fresh?'최근 응답':'응답 오래됨'} · generation {n.generation}{m&&n.id==='gateway'?` · 원본 ${m.originHealthy?'정상':'확인 필요'}`:''}</p>;})}<p className="control-warning">이 상태는 현재 Room의 배포 관측입니다. 위 Traffic Lab은 별도 샘플 환경의 기능 시험이며, 현재 Room이나 고객 원본의 성능 검증을 대신하지 않습니다.</p><Link to="/dashboard/runtime">전체 배포 상태 확인</Link></section>:null}
 {tab==='operations'?<section className="control-card"><h3>{room.name} · 입장 운영</h3><p className="ops-description">{room.hostname} · 활성 입장권 최대 {runtime.limits.maxActiveAdmissionLeases.toLocaleString('ko-KR')}개 · 새 입장권 유효시간 {runtime.limits.admissionTtlSeconds.toLocaleString('ko-KR')}초</p><RoomHealth data={telemetry}/><RuntimeState data={telemetry}/><QueueMetrics data={telemetry}/>
 <p className="ops-metrics-note"><strong>창을 닫아도 입장권은 즉시 반환되지 않습니다.</strong> 최대 입장권이 모두 사용 중이면 기존 입장권이 만료되고 추가 확인 시간(30초)이 지나야 다음 사람이 입장할 수 있습니다. 유효시간 변경은 새로 발급하는 입장권부터 적용됩니다. 유효 입장권은 입장 준비 중인 예약을 제외한 값이며 현재 접속자 수와 다릅니다.</p>
 {telemetry.issues.length?<aside className="ops-issues"><h3>확인이 필요한 상태</h3><ul>{telemetry.issues.map((issue,index)=><li key={index}>{issue.message}</li>)}</ul></aside>:null}
 <details className="operator-details"><summary>기술 정보 보기</summary><p>Room {room.id} · revision {runtime.revision} · epoch {runtime.epoch} · mode {runtime.mode}</p><p className="ops-last-observation">Coordinator · {telemetry.coordinator.node?`${new Date(telemetry.coordinator.node.observedAt).toLocaleTimeString('ko-KR')} 관측`:'응답 없음'} · Gateway · {telemetry.gateway.node?`${new Date(telemetry.gateway.node.observedAt).toLocaleTimeString('ko-KR')} 관측`:'응답 없음'}<br/>최근 5분 유입 {telemetry.arrivals===null?'관측 창 수집 중 / 미확인':`${telemetry.arrivals.toLocaleString('ko-KR')}건`} · HTTP 5xx는 Gateway가 관측한 Room 요청의 최종 500~599 응답 수입니다.</p></details>
 {view.nodes.filter(n=>n.id==='coordinator').flatMap(n=>n.rooms.filter(m=>m.roomId===room.id&&m.mode==='RECOVERY_HOLD')).map(m=><aside key={m.roomId} className="control-warning" role="status"><strong>입장 중지 · 복구 안전 대기</strong><p>{recoveryReasons[m.recoveryReason]||'공유 상태를 확인 중입니다.'}</p><p>{m.recoveryUntil?'검증 시작 가능 시각: '+new Date(m.recoveryUntil).toLocaleString():'아직 안전 대기 종료 시각을 확인하지 못했습니다.'}</p><p>{m.recoveryValidation?'저장 상태 검증이 실패했습니다. 추정 복구나 자동 입장 재개를 하지 않습니다.':'이 시각이 지나도 대기열 데이터 검사를 통과해야 입장을 재개합니다. 기존 입장권의 만료 시각은 늘리지 않습니다.'}</p></aside>)}
 {can('runtime.operate')?<><div className="ops-mode-actions">{[['auto','AUTO','입장 시작','정해 둔 인원만큼, 먼저 온 사람부터 입장시킵니다.'],['hold','HOLD','입장 잠시 멈춤','신규 입장 배정을 멈춥니다. 기존 입장권은 유지됩니다.'],['safe-drain','DRAINING','남은 사람 입장 후 종료','신규 대기를 막고 남은 대기자를 순서대로 입장시킵니다.']].map(([action,mode,label,description])=>{const selected=runtime.mode===mode;return <div key={action} className={selected?'is-selected':undefined}><button className="secondary" aria-pressed={selected} disabled={busy||!room.active||unobserved} onClick={()=>command(`/rooms/${room.id}/runtime`,'PATCH',{action},`"runtime-${runtime.revision}"`)}>{selected?<span aria-hidden="true">✓ </span>:null}{label}</button><div><p>{description}</p>{selected?<span className="ops-mode-selection">현재 설정 · {telemetry.applied?'적용 완료':'적용 확인 중'}</span>:null}</div></div>;})}</div>
 <p className="control-help">‘남은 사람 입장 후 종료’는 새 대기 접수를 닫고 이미 기다리는 사람을 먼저 입장시킵니다. 대기자가 없고 서비스가 안정되면 대기열 보호를 끕니다. 직접 버튼을 누르면 자동 예약은 잠시 멈춥니다.</p>
 <LimitsForm key={room.id} room={room} runtime={runtime} profile={view.config.profile} busy={busy||unobserved} onCommand={command}/></>:null}<div className="control-actions ops-sensitive-actions">{session.capabilities.some(c=>c.action==='runtime.instant_off')?<button className="secondary" disabled={busy||!room.active||Boolean(refreshError)} onClick={()=>instantOff(room,runtime)}>보호 바로 끄기</button>:null}{session.capabilities.some(c=>c.action==='runtime.new_epoch')?<button className="secondary" disabled={busy||Boolean(refreshError)} onClick={()=>newEpoch(room,runtime)}>전체 대기열 초기화</button>:null}</div></section>:null}
 {tab==='schedule'?<EventSchedule key={room.id} room={room} runtime={runtime} events={events} busy={busy} canWrite={can('events.write')} onCommand={command}/>:null}</>:null}
 {sensitive?<ReauthDialog key={sensitive.key} request={sensitive} csrf={csrf} requireTOTP={totpEnabled} onCancel={()=>setSensitive(null)} onComplete={async()=>{setSensitive(null);try{const out=await controlAPI('/config/delivery');if(mounted.current){setView(out.data);setNotice('재인증한 명령을 저장했습니다. 서비스 적용 상태와 안전 대기 시각을 확인하세요.');}}catch(e){if(mounted.current)setError(e.message);}}}/>:null}
 </div>;
}
