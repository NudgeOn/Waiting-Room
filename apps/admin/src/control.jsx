// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI,newRoom,roomFromForm} from './control-api.js';
import './control.css';
import RuntimeDashboard from './runtime.jsx';

function Input({label,name,value,...props}){return <label className="control-field"><span>{label}</span><input name={name} defaultValue={value} required {...props}/></label>;}
function Editor({room,existing,busy,onSave,canWrite,profile}){
  const [title,setTitle]=useState(room.theme.title),[message,setMessage]=useState(room.theme.message);
  if(!canWrite)return <section className="control-card"><h3>{room.name||'Room 설정'}</h3><dl><dt>호스트</dt><dd>{room.hostname}</dd><dt>보호 경로</dt><dd>{room.protectPrefixes.join(', ')}</dd><dt>분당 입장</dt><dd>{room.limits.admissionsPerMinute}</dd></dl><p>현재 계정은 설정을 조회할 수 있습니다.</p></section>;
  return <form className="control-card" onSubmit={e=>{e.preventDefault();onSave(roomFromForm(e.currentTarget,room));}}>
    <h3>{existing?'Room 초안 편집':'새 Room 초안'}</h3>
    <fieldset disabled={busy}><legend>1. 연결</legend><div className="control-grid">
      <Input label="Room ID" name="id" value={room.id} readOnly={existing} pattern="[a-z](?:[a-z0-9_]|-){0,63}" maxLength={64}/>
      <Input label="표시 이름" name="name" value={room.name} maxLength={120}/>
      <Input label="고객 호스트" name="hostname" value={room.hostname} placeholder="shop.example.com"/>
      <Input label="원본 HTTPS 주소" name="origin" type="url" value={room.origin} placeholder="https://origin.example.com"/>
      <Input label="원본 상태 확인 URL" name="healthURL" type="url" value={room.healthURL} placeholder="https://origin.example.com/health"/>
    </div></fieldset>
    <fieldset disabled={busy}><legend>2. 경로</legend><div className="control-grid"><label className="control-field"><span>보호 경로 (한 줄에 하나)</span><textarea name="protect" defaultValue={room.protectPrefixes.join('\n')} required rows={3}/></label><label className="control-field"><span>제외 경로 (한 줄에 하나)</span><textarea name="exclude" defaultValue={room.excludePrefixes.join('\n')} rows={3}/></label></div><p className="control-help">/shop은 /shop/cart에 적용되지만 /shopping에는 적용되지 않습니다. 제외 경로가 우선합니다.</p></fieldset>
    <fieldset disabled={busy}><legend>3. 유량</legend><div className="control-grid">
      <Input label="최대 활성 입장권 수" name="leases" type="number" value={room.limits.maxActiveAdmissionLeases} min={1} max={profile==='high-scale-100k'?100000:10000}/>
      <Input label="분당 신규 입장 수" name="rate" type="number" value={room.limits.admissionsPerMinute} min={1} max={profile==='high-scale-100k'?60000:6000}/>
      <Input label="입장권 유효 시간 (초)" name="ttl" type="number" value={room.limits.admissionTtlSeconds} min={60} max={3600}/>
    </div><p className="control-help">FIFO · 먼저 온 순서대로 입장합니다. 활성 입장권 수는 현재 접속자 수가 아닙니다.</p></fieldset>
    <fieldset disabled={busy}><legend>4. 대기 화면</legend><div className="control-grid">
      <label className="control-field"><span>Template</span><select defaultValue="calm"><option value="calm">Calm · 기본 제공</option></select></label>
      <label className="control-field"><span>언어</span><select name="locale" defaultValue={room.theme.locale}><option value="ko">한국어</option><option value="en">English</option></select></label>
      <Input label="기본 색상 (HEX)" name="color" value={room.theme.primaryColor} pattern="#[a-fA-F0-9]{6}"/>
      <Input label="안내 제목" name="title" value={room.theme.title} maxLength={120} onChange={e=>setTitle(e.target.value)}/>
      <label className="control-field"><span>안내 문구</span><textarea name="message" defaultValue={room.theme.message} maxLength={1000} onChange={e=>setMessage(e.target.value)} rows={3}/></label>
    </div><aside className="control-preview" aria-label="대기 문구 미리보기"><small>CALM · 문구 미리보기</small><h4>{title}</h4><p>{message}</p><small>실제 Gateway 렌더링·색상 적용 검증은 배포 단계에서 진행합니다.</small></aside></fieldset>
    <p className="control-warning">초안 저장만 수행합니다. 원본 연결 검사·서명 배포·대기열 활성화는 실행하지 않습니다.</p>
    <input type="hidden" name="active-present" value="1"/><label className="control-field"><span><input type="checkbox" name="active" defaultChecked={room.active} disabled={busy}/> 배포 시 이 Room 보호 활성화 (첫 배포는 HOLD)</span></label>
    <button className="primary" disabled={busy}>{busy?'저장 중…':'초안 저장'}</button>
  </form>;
}
export default function Control({session,csrf,onBack,persistent=false}){
  const [runtimeOpen,setRuntimeOpen]=useState(false);
  const [snapshot,setSnapshot]=useState(null),[audit,setAudit]=useState([]),[selected,setSelected]=useState(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
  const lock=useRef(false),pending=useRef(null),mounted=useRef(true),heading=useRef(null);
  const canWrite=Boolean(csrf)&&session.capabilities.some(c=>c.action==='config.write'&&!c.requiresReauthentication);
  useEffect(()=>{mounted.current=true;let live=true;Promise.all([controlAPI('/config/draft'),controlAPI('/audit-events')]).then(([config,events])=>{if(live){setSnapshot(config);setAudit(events.data.items);}}).catch(e=>{if(live)setError(e.message);});heading.current?.focus();return()=>{live=false;mounted.current=false;};},[]);
  async function load(){if(lock.current)return;lock.current=true;setBusy(true);setError('');try{const [config,events]=await Promise.all([controlAPI('/config/draft'),controlAPI('/audit-events')]);if(mounted.current){setSnapshot(config);setAudit(events.data.items);setSelected(null);pending.current=null;setNotice('최신 저장 초안을 불러왔습니다.');}}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
  async function save(room){if(lock.current||!snapshot)return;lock.current=true;setBusy(true);setError('');setNotice('');const exists=snapshot.data.rooms.some(r=>r.id===room.id);if(!selected?.existing&&exists){setError('이미 사용 중인 Room ID입니다.');setBusy(false);lock.current=false;return;}
    const body={...snapshot.data,rooms:exists?snapshot.data.rooms.map(r=>r.id===room.id?room:r):[...snapshot.data.rooms,room]};const serialized=JSON.stringify(body);
    if(!pending.current||pending.current.body!==serialized||pending.current.etag!==snapshot.etag)pending.current={body:serialized,etag:snapshot.etag,key:crypto.randomUUID()};
    try{const out=await controlAPI('/config/draft',{method:'PUT',body,csrf,etag:pending.current.etag,key:pending.current.key});if(mounted.current){setSnapshot(out);setSelected({room,existing:true});pending.current=null;setNotice('초안을 저장했습니다. 실제 대기열에는 아직 적용하지 않았습니다.');}const events=await controlAPI('/audit-events');if(mounted.current)setAudit(events.data.items);}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
  if(runtimeOpen&&snapshot)return <RuntimeDashboard csrf={csrf} session={session} draft={snapshot} onBack={()=>setRuntimeOpen(false)}/>;
  return <div className="control-workspace"><div className="control-toolbar"><div><h2 tabIndex={-1} ref={heading}>Room 초안 관리</h2><p>설정 준비 → 검토 → 별도 배포. 저장만으로는 운영 설정을 바꾸지 않습니다.</p></div><button className="text-button" onClick={onBack} disabled={busy}>세션으로 돌아가기</button></div>
    <p className="control-warning" role="note">{persistent?'로컬 Docker · Beta 개발 중 · 계정과 초안은 재시작 후에도 유지됩니다. 실시간 운영 화면에서 별도로 배포하고 적용 상태를 확인하세요.':'로컬 개발 환경 · Beta 미완료 · 서버를 종료하면 이 실험의 계정과 초안이 삭제됩니다.'}</p>
    {persistent?<button className="primary" disabled={busy||!snapshot} onClick={()=>setRuntimeOpen(true)}>실시간 운영</button>:null}
    {error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
    <div className="control-actions"><button className="secondary" disabled={busy} onClick={load}>최신 초안 불러오기</button>{canWrite?<button className="secondary" disabled={busy||!snapshot} onClick={()=>{setSelected({room:newRoom(),existing:false});setNotice('');}}>새 Room 초안</button>:null}</div>
    {!snapshot?<p role="status">{error?'초안을 표시할 수 없습니다.':'초안 불러오는 중…'}</p>:<><section className="control-card"><h3>저장된 Room <span className="control-meta">{snapshot.data.rooms.length}개 · revision {snapshot.data.revision}</span></h3>{snapshot.data.rooms.length===0?<p>아직 Room이 없습니다. 연결 정보와 보호 경로부터 준비하세요.</p>:<ul className="room-list">{snapshot.data.rooms.map(room=><li key={room.id}><button disabled={busy} onClick={()=>setSelected({room,existing:true})}><strong>{room.name}</strong><span>{room.hostname}</span><small>초안 · {room.protectPrefixes.join(', ')}</small></button></li>)}</ul>}</section>
      {selected?<Editor key={selected.room.publicId+':'+snapshot.data.revision} room={selected.room} existing={selected.existing} canWrite={canWrite} profile={snapshot.data.profile} busy={busy} onSave={save}/>:null}
      <section className="control-card"><h3>최근 설정 감사 로그</h3><p className="control-help">설정 본문·비밀키 대신 변경 digest를 저장합니다. 최근 50건입니다.</p>{audit.length===0?<p>아직 변경 기록이 없습니다.</p>:<ol className="audit-list">{audit.map(event=><li key={event.id}><strong>{event.result==='saved_draft'?'초안 저장':'변경 거부'}</strong><span>{event.actorId} · revision {event.revision}</span><time dateTime={event.at}>{new Date(event.at).toLocaleString('ko-KR')}</time></li>)}</ol>}</section></>}
  </div>;
}
