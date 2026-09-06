// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {Link} from './navigation.jsx';
export default function Dashboard({persistent,canWrite}){
  const [data,setData]=useState(null),[error,setError]=useState('');const heading=useRef(null);
  useEffect(()=>{let live=true,timer;heading.current?.focus();async function refresh(){try{const [draft,delivery]=await Promise.all([controlAPI('/config/draft'),persistent?controlAPI('/config/delivery'):null]);if(live){setData({draft:draft.data,delivery:delivery?.data});setError('');}}catch(e){if(live)setError(e.message);}if(live)timer=setTimeout(refresh,5000);}refresh();return()=>{live=false;clearTimeout(timer);};},[persistent]);
  return <section className="control-workspace"><h2 ref={heading} tabIndex={-1}>대시보드</h2><p>설정 준비부터 실제 적용까지, 현재 상태를 확인하세요.</p>{error?<p role="alert" className="error">{error} · 마지막 조회값을 정상 상태로 판단하지 마세요.</p>:null}
    {!data?<p role="status">운영 상태 불러오는 중…</p>:<><div className="control-grid"><section className="control-card"><h3>설정 상태</h3><p>저장 초안 revision {data.draft.revision}</p><p>{persistent?`배포 revision ${data.delivery?.revision??'—'} · ${data.delivery?.state==='applied'?'두 서비스 적용 확인':'적용 대기 / 확인 필요'}`:'개발 Lab · 실제 서비스 배포 없음'}</p>{persistent?<Link to="/dashboard/runtime">배포와 적용 상태 확인</Link>:null}</section><section className="control-card"><h3>서비스 건강 상태</h3>{persistent?(data.delivery?.nodes.length?data.delivery.nodes.map(n=><p key={n.id}>{n.id} · {n.fresh?'최근 응답':'응답 오래됨'} · generation {n.generation}</p>):<p>서비스 응답을 기다리고 있습니다.</p>):<p>Lab에는 Gateway·Coordinator가 연결되지 않습니다.</p>}</section></div>
    <section className="control-card"><h3>Room 목록 · {data.draft.rooms.length}개</h3><p className="control-help">초안의 활성화 표시와 실제 적용 모드는 다를 수 있습니다.</p>{canWrite?<Link to="/rooms/new">새 Room 만들기</Link>:null}{data.draft.rooms.length===0?<p>아직 Room이 없습니다. 연결 정보와 보호 경로부터 준비하세요.</p>:<ul className="dashboard-rooms">{data.draft.rooms.map(room=>{const runtime=data.delivery?.runtimes.find(r=>r.roomId===room.id)?.runtime;return <li key={room.id}><Link to={`/rooms/${room.id}/operations`}><strong>{room.name}</strong><span>{room.hostname}</span><small>실제 모드 {runtime?.mode??'미배포'} · 초안 {room.active?'활성화 예정':'비활성'}</small></Link></li>;})}</ul>}</section></>}
  </section>;
}
