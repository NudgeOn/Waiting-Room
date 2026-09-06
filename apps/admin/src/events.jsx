// SPDX-License-Identifier: Apache-2.0
import React,{useState} from 'react';
import {eventFromForm,eventLabels,localEventTime} from './events.js';

function EventForm({room,runtime,event,busy,onCommand,onDone}){
 const [revision]=useState(runtime.revision),[error,setError]=useState('');
 async function submit(e){
  e.preventDefault();setError('');
  let body;try{body=eventFromForm(new FormData(e.currentTarget));}catch(e){setError(e.message);return;}
  const path=event?`/events/${event.id}`:`/rooms/${room.id}/events`;
  if(await onCommand(path,event?'PUT':'POST',body,`"runtime-${revision}"`))onDone();
 }
 return <form onSubmit={submit}><fieldset disabled={busy||!room.active}><legend>{event?'예약 수정':'새 예약'}</legend>
 <p className="control-help">편집 기준 revision {revision}. 시작한 예약을 수정하려면 모든 단계를 미래 시각으로 다시 지정하세요.</p>
 {revision!==runtime.revision?<p className="control-warning">운영 상태가 변경됐습니다. 입력값을 보존했습니다. 취소 후 최신 기준으로 다시 편집하거나 제출해 충돌을 확인하세요.</p>:null}
 {error?<p className="error" role="alert">{error}</p>:null}
 <div className="control-grid">{[['prequeueAt','사전 대기 시작'],['admitAt','입장 시작'],['drainAt','안전 종료 시작']].map(([name,label])=><label className="control-field" key={name}><span>{label}</span><input type="datetime-local" step="1" name={name} required defaultValue={event?localEventTime(event[name]):''}/></label>)}</div>
 <div className="control-actions"><button className="secondary">{event?'예약 수정 저장':'예약 저장'}</button><button type="button" className="text-button" onClick={onDone}>{event?'수정 취소':'입력 초기화'}</button></div>
 </fieldset></form>;
}

export default function EventSchedule({room,runtime,events,busy,canWrite,onCommand}){
 const [editing,setEditing]=useState(null),[formKey,setFormKey]=useState(0);
 const finish=()=>{setEditing(null);setFormKey(k=>k+1);};
 return <section className="control-card"><h3>예약</h3><p className="control-help">입력 시간대: {Intl.DateTimeFormat().resolvedOptions().timeZone}. 서버에는 UTC로 저장합니다. 겹치는 예약은 거부됩니다. 취소는 현재 유량이나 모드를 바꾸지 않습니다.</p>
 {canWrite?<EventForm key={`${formKey}-${editing?.id??'new'}`} room={room} runtime={runtime} event={editing} busy={busy} onCommand={onCommand} onDone={finish}/>:null}
 {events.length===0?<p>등록된 예약이 없습니다.</p>:null}
 <ul className="audit-list">{events.map(event=><li key={event.id} data-event-id={event.id}><strong>{eventLabels[event.state]??'상태 확인 필요'}</strong><span>대기 {new Date(event.prequeueAt).toLocaleString()} → 입장 {new Date(event.admitAt).toLocaleString()} → 종료 {new Date(event.drainAt).toLocaleString()}</span>
 {canWrite&&!['cancelled','completed'].includes(event.state)?<div className="control-actions"><button disabled={busy||!room.active} onClick={()=>{setEditing(event);setFormKey(k=>k+1);}}>예약 수정</button>{event.state==='paused_by_override'?<button disabled={busy||!room.active} onClick={()=>onCommand(`/events/${event.id}/resume`,'POST',{},`"runtime-${runtime.revision}"`)}>예약 재개</button>:null}<button disabled={busy} onClick={()=>onCommand(`/events/${event.id}`,'DELETE',{},`"runtime-${runtime.revision}"`)}>예약 취소</button></div>:null}</li>)}</ul>
 </section>;
}
