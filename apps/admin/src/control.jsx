// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI,newRoom,roomFromForm} from './control-api.js';
import './control.css';
import RuntimeDashboard from './runtime.jsx';
import Security from './security.jsx';
import {auditDisplay} from './audit.js';
import {navigate} from './routes.js';
import RoomWizard from './room-wizard.jsx';

function Input({label,name,value,...props}){return <label className="control-field"><span>{label}</span><input name={name} defaultValue={value} required {...props}/></label>;}
function Editor({room,existing,busy,onSave,canWrite,profile}){
  const [title,setTitle]=useState(room.theme.title),[message,setMessage]=useState(room.theme.message);
  const [step,setStep]=useState(0),[review,setReview]=useState(null);const form=useRef(null),stepHeading=useRef(null);
  const wizard=!existing,steps=['연결','경로','유량','대기 화면','검토'];
  useEffect(()=>{if(wizard)stepHeading.current?.focus();},[step,wizard]);
  function advance(){const fields=form.current.querySelectorAll(`fieldset[data-step="${step}"] input,fieldset[data-step="${step}"] textarea,fieldset[data-step="${step}"] select`);for(const field of fields){if(!field.reportValidity())return;}if(step===3)setReview(roomFromForm(form.current,room));setStep(Math.min(4,step+1));}
  function submit(e){e.preventDefault();if(wizard&&step<4){advance();return;}const invalid=[...form.current.elements].find(field=>field.willValidate&&!field.validity.valid);if(invalid){const target=Number(invalid.closest('fieldset')?.dataset.step??4);setStep(target);requestAnimationFrame(()=>invalid.reportValidity());return;}onSave(roomFromForm(form.current,room));}
  if(!canWrite)return <section className="control-card"><h3>{room.name||'Room 설정'}</h3><dl><dt>호스트</dt><dd>{room.hostname}</dd><dt>보호 경로</dt><dd>{room.protectPrefixes.join(', ')}</dd><dt>분당 입장</dt><dd>{room.limits.admissionsPerMinute}</dd></dl><p>현재 계정은 설정을 조회할 수 있습니다.</p></section>;
  return <form className="control-card" ref={form} noValidate onSubmit={submit}>
    <h3>{existing?'Room 초안 편집':'새 Room 초안'}</h3>
    {wizard?<><ol className="wizard-steps" aria-label="Room 생성 단계">{steps.map((label,index)=><li key={label} aria-current={step===index?'step':undefined}>{index+1}. {label}</li>)}</ol><h4 ref={stepHeading} tabIndex={-1}>{step+1}/5 · {steps[step]}</h4><p className="control-help">단계를 이동해도 입력값은 유지됩니다. 저장 전까지 운영에는 반영되지 않습니다.</p></>:null}
    <fieldset data-step="0" hidden={wizard&&step!==0} disabled={busy}><legend>1. 연결</legend><div className="control-grid">
      <Input label="Room ID" name="id" value={room.id} readOnly={existing} pattern="[a-z](?:[a-z0-9_]|-){0,63}" maxLength={64}/>
      <Input label="표시 이름" name="name" value={room.name} maxLength={120}/>
      <Input label="고객 호스트" name="hostname" value={room.hostname} placeholder="shop.example.com"/>
      <Input label="원본 HTTPS 주소" name="origin" type="url" value={room.origin} placeholder="https://origin.example.com"/>
      <Input label="원본 상태 확인 URL" name="healthURL" type="url" value={room.healthURL} placeholder="https://origin.example.com/health"/>
    </div></fieldset>
    <fieldset data-step="1" hidden={wizard&&step!==1} disabled={busy}><legend>2. 경로</legend><div className="control-grid"><label className="control-field"><span>보호 경로 (한 줄에 하나)</span><textarea name="protect" defaultValue={room.protectPrefixes.join('\n')} required rows={3}/></label><label className="control-field"><span>제외 경로 (한 줄에 하나)</span><textarea name="exclude" defaultValue={room.excludePrefixes.join('\n')} rows={3}/></label></div><p className="control-help">/shop은 /shop/cart에 적용되지만 /shopping에는 적용되지 않습니다. 제외 경로가 우선합니다.</p></fieldset>
    <fieldset data-step="2" hidden={wizard&&step!==2} disabled={busy}><legend>3. 유량</legend><div className="control-grid">
      <Input label="최대 활성 입장권 수" name="leases" type="number" value={room.limits.maxActiveAdmissionLeases} min={1} max={profile==='high-scale-100k'?100000:10000}/>
      <Input label="분당 신규 입장 수" name="rate" type="number" value={room.limits.admissionsPerMinute} min={1} max={profile==='high-scale-100k'?60000:6000}/>
      <Input label="입장권 유효 시간 (초)" name="ttl" type="number" value={room.limits.admissionTtlSeconds} min={60} max={3600}/>
    </div><p className="control-help">FIFO · 먼저 온 순서대로 입장합니다. 활성 입장권 수는 현재 접속자 수가 아닙니다.</p></fieldset>
    <fieldset data-step="3" hidden={wizard&&step!==3} disabled={busy}><legend>4. 대기 화면</legend><div className="control-grid">
      <label className="control-field"><span>Template</span><select defaultValue="calm"><option value="calm">Calm · 기본 제공</option></select></label>
      <label className="control-field"><span>언어</span><select name="locale" defaultValue={room.theme.locale}><option value="ko">한국어</option><option value="en">English</option></select></label>
      <Input label="기본 색상 (HEX)" name="color" value={room.theme.primaryColor} pattern="#[a-fA-F0-9]{6}"/>
      <Input label="안내 제목" name="title" value={room.theme.title} maxLength={120} onChange={e=>setTitle(e.target.value)}/>
      <label className="control-field"><span>안내 문구</span><textarea name="message" defaultValue={room.theme.message} maxLength={1000} onChange={e=>setMessage(e.target.value)} rows={3}/></label>
    </div><aside className="control-preview" aria-label="대기 문구 미리보기"><small>CALM · 문구 미리보기</small><h4>{title}</h4><p>{message}</p><small>실제 Gateway 렌더링·색상 적용 검증은 배포 단계에서 진행합니다.</small></aside></fieldset>
    <section hidden={wizard&&step!==4}>
    {wizard&&review?<><h4>저장 전 검토</h4><dl><dt>Room</dt><dd>{review.name} · {review.id}</dd><dt>연결</dt><dd>{review.hostname} → {review.origin}</dd><dt>보호 / 제외</dt><dd>{review.protectPrefixes.join(', ')} / {review.excludePrefixes.join(', ')||'없음'}</dd><dt>유량</dt><dd>FIFO · {review.limits.admissionsPerMinute}/분 · 입장권 최대 {review.limits.maxActiveAdmissionLeases}</dd><dt>대기 화면</dt><dd>Calm · {review.theme.locale} · {review.theme.title}</dd></dl></>:null}
    <p className="control-warning">초안 저장만 수행합니다. 원본 연결 검사·서명 배포·대기열 활성화는 실행하지 않습니다. 샘플 환경의 Quick 20·Smoke 1K는 Traffic Lab에서 별도로 실행할 수 있습니다.</p>
    <input type="hidden" name="active-present" value="1"/><label className="control-field"><span><input type="checkbox" name="active" defaultChecked={room.active} disabled={busy}/> 배포 시 이 Room 보호 활성화 (첫 배포는 HOLD)</span></label>
    <button className="primary" disabled={busy}>{busy?'저장 중…':'초안 저장'}</button>
    </section>
    {wizard?<div className="control-actions">{step>0?<button type="button" className="secondary" disabled={busy} onClick={()=>setStep(step-1)}>이전 단계</button>:null}{step<4?<button type="button" className="primary" disabled={busy} onClick={advance}>다음 단계</button>:null}</div>:null}
  </form>;
}
export default function Control({session,csrf,onBack,onEnrollment,onSessionChanged,persistent=false,route={page:'rooms'}}){
  const [snapshot,setSnapshot]=useState(null),[audit,setAudit]=useState([]),[auditCursor,setAuditCursor]=useState(''),[auditBusy,setAuditBusy]=useState(false),[selection,setSelected]=useState(()=>route.page==='new'?{room:newRoom(),existing:false}:null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(()=>history.state?.wrNotice==='draft-saved'?'초안을 저장했습니다. 실제 대기열에는 아직 적용하지 않았습니다.':'');
  useEffect(()=>{if(history.state?.wrNotice==='draft-saved')history.replaceState(null,'',location.href);},[]);
  const routeRoom=snapshot?.data.rooms.find(r=>r.id===route.roomId);
  const selected=selection??(routeRoom?{room:routeRoom,existing:true}:null);
  const lock=useRef(false),pending=useRef(null),mounted=useRef(true),heading=useRef(null);
  const canReadSecurity=session.capabilities.some(c=>c.action==='security.read');
  const auditLock=useRef(false),auditVersion=useRef(0);
  function acceptAudit(events){auditVersion.current++;setAudit(events.data.items);setAuditCursor(events.data.nextCursor??'');}
  async function loadAudit(){if(auditLock.current||!auditCursor)return;auditLock.current=true;const version=auditVersion.current;setAuditBusy(true);setError('');try{const out=await controlAPI('/audit-events?cursor='+encodeURIComponent(auditCursor));if(mounted.current&&version===auditVersion.current){setAudit(items=>{const ids=new Set(items.map(x=>x.id));return [...items,...out.data.items.filter(x=>!ids.has(x.id))];});setAuditCursor(out.data.nextCursor??'');}}catch(e){if(mounted.current)setError(e.message);}finally{auditLock.current=false;if(mounted.current)setAuditBusy(false);}}
  const canWrite=Boolean(csrf)&&session.capabilities.some(c=>c.action==='config.write'&&!c.requiresReauthentication);
  useEffect(()=>{mounted.current=true;let live=true;Promise.all([controlAPI('/config/draft'),controlAPI('/audit-events'),persistent&&route.page==='new'?controlAPI('/installation'):Promise.resolve(null)]).then(([config,events,installation])=>{if(live){if(installation?.data.report)setSelected(value=>({...value,room:{...value.room,limits:installation.data.report.plan.input.limits}}));setSnapshot(config);acceptAudit(events);}}).catch(e=>{if(live)setError(e.message);});heading.current?.focus();return()=>{live=false;mounted.current=false;};},[]);
  async function load(){if(lock.current)return;lock.current=true;setBusy(true);setError('');try{const [config,events]=await Promise.all([controlAPI('/config/draft'),controlAPI('/audit-events')]);if(mounted.current){setSnapshot(config);acceptAudit(events);if(route.page!=='new')setSelected(null);pending.current=null;setNotice('최신 저장 초안을 불러왔습니다.');}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
  async function save(room){if(lock.current||!snapshot)return;lock.current=true;setBusy(true);setError('');setNotice('');const exists=snapshot.data.rooms.some(r=>r.id===room.id);if(!selected?.existing&&exists){setError('이미 사용 중인 Room ID입니다.');setBusy(false);lock.current=false;return;}
    const body={...snapshot.data,rooms:exists?snapshot.data.rooms.map(r=>r.id===room.id?room:r):[...snapshot.data.rooms,room]};const serialized=JSON.stringify(body);
    if(!pending.current||pending.current.body!==serialized||pending.current.etag!==snapshot.etag)pending.current={body:serialized,etag:snapshot.etag,key:crypto.randomUUID()};
    try{const out=await controlAPI('/config/draft',{method:'PUT',body,csrf,etag:pending.current.etag,key:pending.current.key});if(mounted.current){setSnapshot(out);setSelected({room,existing:true});pending.current=null;setNotice('초안을 저장했습니다. 실제 대기열에는 아직 적용하지 않았습니다.');if(route.page==='new'){navigate(`/rooms/${room.id}/settings`,{saved:true});return;}}const events=await controlAPI('/audit-events');if(mounted.current)acceptAudit(events);}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
  if((route.page==='runtime'||(route.page==='room'&&route.tab!=='settings'))&&snapshot)return persistent?<RuntimeDashboard csrf={csrf} session={session} draft={snapshot} roomId={route.roomId} tab={route.tab} onBack={()=>navigate('/rooms')}/>:<section className="control-card"><h2>실제 운영 연결 필요</h2><p>이 인증 Lab에는 Gateway·Coordinator가 없습니다. 로컬 Docker 설치에서 운영·일정·검증을 사용할 수 있습니다.</p></section>;
  if(route.page==='security'&&!canReadSecurity)return <section className="control-card"><h2 ref={heading} tabIndex={-1}>보안 설정 권한이 필요합니다</h2><p>사용자와 보안 설정은 Admin 계정에서 관리할 수 있습니다.</p><button className="secondary" onClick={()=>navigate('/rooms')}>Room 목록으로</button></section>;
  if(route.page==='security')return persistent?<Security csrf={csrf} session={session} onBack={()=>navigate('/rooms')} onEnrollment={onEnrollment} onSessionChanged={onSessionChanged}/>:<p role="alert">로컬 Docker 설치에서 보안 설정을 사용할 수 있습니다.</p>;
  if(route.page==='new')return <div className="control-workspace rw-workspace"><div className="rw-page-title"><div><span className="rw-draft-badge">초안 작성</span><h2 ref={heading} tabIndex={-1}>새 Room 만들기</h2><p>서비스 연결부터 대기 화면까지, 다섯 단계로 준비하세요.</p></div><button type="button" className="text-button" disabled={busy} onClick={()=>navigate('/rooms')}>Room 목록</button></div>
    {!snapshot?<>{error?<p role="alert" className="error">{error}</p>:<p role="status">저장된 설정을 불러오는 중…</p>}{error?<button type="button" className="secondary" onClick={load} disabled={busy}>다시 불러오기</button>:null}</>:!canWrite?<section className="control-card"><h3>Room 생성 권한이 필요합니다</h3><p>현재 계정은 설정을 조회할 수 있습니다. Admin 계정에서 새 Room을 만들 수 있습니다.</p><button type="button" className="secondary" onClick={()=>navigate('/rooms')}>Room 목록으로</button></section>:<>{notice?<p className="control-success" role="status">{notice}</p>:null}<RoomWizard key={selected.room.publicId} room={selected.room} rooms={snapshot.data.rooms} profile={snapshot.data.profile} busy={busy} onSave={save} onCancel={()=>navigate('/rooms')} onReload={load} saveError={error}/></>}
  </div>;
  return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>Room 초안 관리</h2><p>설정 준비 → 검토 → 별도 배포. 저장만으로는 운영 설정을 바꾸지 않습니다.</p></div><button className="text-button" onClick={onBack} disabled={busy}>세션으로 돌아가기</button></div>
    <p className="control-warning" role="note">{persistent?'로컬 Docker · Beta 개발 중 · 계정과 초안은 재시작 후에도 유지됩니다. 실시간 운영 화면에서 별도로 배포하고 적용 상태를 확인하세요.':'로컬 개발 환경 · Beta 미완료 · 서버를 종료하면 이 실험의 계정과 초안이 삭제됩니다.'}</p>
    {persistent?<button className="primary" disabled={busy||!snapshot} onClick={()=>navigate('/dashboard/runtime')}>실시간 운영</button>:null}
    {persistent&&session.capabilities.some(c=>c.action==='security.read')?<button className="secondary" disabled={busy||!csrf} onClick={()=>navigate('/settings')}>사용자 · 보안 설정</button>:null}
    {error?<p className="error" role="alert">{error}</p>:null}{notice&&snapshot?<p className="control-success" role="status">{notice}</p>:null}
    <div className="control-actions"><button className="secondary" disabled={busy} onClick={load}>최신 초안 불러오기</button>{canWrite?<button className="secondary" disabled={busy||!snapshot} onClick={()=>{if(route.page==='new'){setSelected({room:newRoom(),existing:false});setNotice('');}else navigate('/rooms/new');}}>새 Room 초안</button>:null}</div>
    {!snapshot?<p role="status">{error?'초안을 표시할 수 없습니다.':'초안 불러오는 중…'}</p>:<><section className="control-card"><h3>저장된 Room <span className="control-meta">{snapshot.data.rooms.length}개 · revision {snapshot.data.revision}</span></h3>{snapshot.data.rooms.length===0?<p>아직 Room이 없습니다. 연결 정보와 보호 경로부터 준비하세요.</p>:<ul className="room-list">{snapshot.data.rooms.map(room=><li key={room.id}><button disabled={busy} onClick={()=>navigate(`/rooms/${room.id}/settings`)}><strong>{room.name}</strong><span>{room.hostname}</span><small>초안 · {room.protectPrefixes.join(', ')}</small></button></li>)}</ul>}</section>
      {route.page==='room'&&!routeRoom?<p role="alert">이 Room을 찾을 수 없습니다. 저장된 Room 목록에서 다시 선택하세요.</p>:null}
      {selected?<Editor key={selected.room.publicId+':'+snapshot.data.revision} room={selected.room} existing={selected.existing} canWrite={canWrite} profile={snapshot.data.profile} busy={busy} onSave={save}/>:null}
      <section className="control-card"><h3>최근 운영 감사 로그</h3><p className="control-help">설정 본문·비밀키 대신 변경 digest를 저장합니다. 50건씩 최신순으로 불러옵니다.</p>{audit.length===0?<p>아직 변경 기록이 없습니다.</p>:<ol className="audit-list">{audit.map(event=>{const display=auditDisplay(event);return <li key={event.id}><strong>{display.action} · {display.result}</strong><span>{event.actorId} · {event.targetId} · revision {event.revision}</span><time dateTime={display.dateTime}>{display.time}</time></li>;})}</ol>}{auditCursor?<button className="secondary" disabled={auditBusy} onClick={loadAudit}>{auditBusy?'감사 기록 불러오는 중…':'이전 감사 기록 더 보기'}</button>:null}<p className="control-help" role="status">감사 기록 {audit.length}건 표시</p></section></>}
  </div>;
}
