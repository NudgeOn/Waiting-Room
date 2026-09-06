// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {prepareAction} from './security-api.js';
import ReauthDialog from './reauth.jsx';
import {USER_PATTERN} from './api.js';

function UserEditor({user,lastAdmin,onAction}) {
  return <li><h4>{user.id}</h4><p>{user.role} · {user.enabled?'활성':'비활성'} · TOTP {user.totpEnrolled?'등록됨':'미등록'}</p>
  <form onSubmit={event=>{event.preventDefault();const data=new FormData(event.currentTarget);onAction({path:`/users/${user.id}`,method:'PATCH',target:user.id,action:'users.write',body:{role:data.get('role'),enabled:data.has('enabled')},etag:user.etag,label:`${user.id}의 역할과 활성 상태 변경. 이 사용자의 모든 세션을 종료합니다.`});}}>
    <label className="control-field"><span>{user.id} 역할</span><select aria-label={`${user.id} 역할`} name="role" defaultValue={user.role} disabled={lastAdmin}><option value="admin">Admin</option><option value="operator">Operator</option><option value="viewer">Viewer</option></select></label>
    <label className="check"><input type="checkbox" name="enabled" defaultChecked={user.enabled} disabled={lastAdmin}/>{user.id} 활성</label>
    <div className="control-actions"><button className="secondary" disabled={lastAdmin}>계정 변경</button>
    <button className="secondary" type="button" disabled={lastAdmin} onClick={()=>onAction({path:`/users/${user.id}/totp-reset`,method:'POST',target:user.id,action:'security.totp.reset',body:{},etag:user.etag,label:`${user.id}의 TOTP와 복구 코드를 삭제하고 모든 세션을 종료합니다. 다음 로그인에서 다시 등록해야 합니다.`})}>TOTP 초기화</button>
    <button className="secondary" type="button" disabled={lastAdmin} onClick={()=>onAction({path:`/users/${user.id}`,method:'DELETE',target:user.id,action:'users.write',etag:user.etag,label:`${user.id} 삭제. 비밀번호·TOTP·세션은 제거하며 감사 이력용 ID는 다시 사용할 수 없습니다.`})}>계정 삭제</button></div>
    {lastAdmin?<p className="control-help">마지막 활성 Admin입니다. 다른 Admin을 먼저 추가하세요.</p>:null}
  </form></li>;
}
export default function Security({session,csrf,onBack,onEnrollment,onSessionChanged}) {
  const [users,setUsers]=useState(null),[policy,setPolicy]=useState(null),[request,setRequest]=useState(null),[error,setError]=useState(''),[notice,setNotice]=useState('');
  const heading=useRef(null),mounted=useRef(true);
  async function refresh(){try{const [u,p]=await Promise.all([controlAPI('/users'),controlAPI('/security/totp')]);if(mounted.current){setUsers(u.data.users);setPolicy(p);}}catch(e){if(mounted.current)setError(e.message);}}
  useEffect(()=>{mounted.current=true;heading.current?.focus();refresh();return()=>{mounted.current=false;};},[]);
  function action(input){setError('');setNotice('');setRequest(prepareAction(input));}
  async function complete(out){const enrollment=request.path.endsWith('/enrollment');setRequest(null);if(enrollment){onEnrollment(out.data);return;}
    try{await onSessionChanged(out.csrf||csrf);
      if(mounted.current){setNotice('변경을 완료했습니다. 계정 변경 대상과 정책 변경 전 세션은 다시 로그인해야 합니다.');await refresh();}
    }catch(e){if(mounted.current)setError(e.message);}
  }
  const enabledAdmins=users?.filter(user=>user.role==='admin'&&user.enabled).length;
  return <div className="control-workspace"><div className="control-toolbar"><h2 tabIndex={-1} ref={heading}>사용자 · 보안 설정</h2><button className="text-button" disabled={Boolean(request)} onClick={onBack}>초안으로 돌아가기</button></div>
    {error?<p className="error" role="alert">{error}</p>:null}{notice?<p className="control-success" role="status">{notice}</p>:null}
    <section className="control-card"><h3>TOTP 정책</h3>{policy?<><p>설치 전체: <strong>{policy.data.enabled?'ON':'OFF'}</strong> · {policy.data.mode} · revision {policy.data.version}</p><p className="control-help">변경은 현재 Admin 재인증이 필요합니다. 현재 Admin 세션만 교체하고 다른 모든 세션은 종료합니다. 응답을 받지 못했다면 다시 로그인해 정책을 확인하세요.</p>
    {!policy.data.enabled&&!policy.data.enrolled?<button className="primary" onClick={()=>action({path:'/security/totp/enrollment',method:'POST',target:'totp-enrollment',action:'security.totp.write',body:{},etag:policy.etag,label:'현재 Admin의 TOTP 등록 준비. 등록 완료 후 별도로 전역 ON을 실행합니다.'})}>ON 전 현재 Admin TOTP 등록</button>:<button className="secondary" disabled={policy.data.mode==='forced_on'} onClick={()=>action({path:'/security/totp',method:'PUT',target:'totp',action:'security.totp.write',body:{enabled:!policy.data.enabled},etag:policy.etag,label:`설치 전체 TOTP ${policy.data.enabled?'OFF':'ON'} 전환. 다른 모든 세션이 종료됩니다.`})}>TOTP {policy.data.enabled?'OFF로 변경':'ON으로 변경'}</button>}
    {policy.data.mode==='forced_on'?<p>설치 정책으로 ON이 고정되어 있습니다.</p>:null}</>:<p role="status">정책 확인 중…</p>}</section>
    <section className="control-card"><h3>계정 만들기</h3><form onSubmit={event=>{event.preventDefault();const form=event.currentTarget,data=new FormData(form);action({path:'/users',method:'POST',target:String(data.get('id')),action:'users.write',body:{id:data.get('id'),role:data.get('role'),password:data.get('password')},label:`${data.get('id')} 계정 생성 (${data.get('role')}). TOTP ON이면 첫 로그인에서 등록이 필요합니다.`});form.reset();}}>
      <fieldset disabled={!policy||Boolean(request)}><legend>새 계정</legend><div className="control-grid"><label className="control-field"><span>새 사용자 ID</span><input name="id" required pattern={USER_PATTERN} maxLength={128} autoComplete="off"/></label><label className="control-field"><span>새 사용자 비밀번호</span><input name="password" type="password" required minLength={15} maxLength={1024} autoComplete="new-password"/></label><label className="control-field"><span>새 사용자 역할</span><select aria-label="새 사용자 역할" name="role" defaultValue="viewer"><option value="viewer">Viewer</option><option value="operator">Operator</option><option value="admin">Admin</option></select></label></div><button className="primary">계정 생성 준비</button></fieldset></form></section>
    <section className="control-card"><h3>사용자</h3><p>현재 로그인: {session.userId}. 삭제한 ID는 재사용하지 않습니다.</p><button className="text-button" disabled={Boolean(request)} onClick={refresh}>최신 계정 불러오기</button><ul className="user-list">{users?.map(user=><UserEditor key={`${user.id}:${user.revision}`} user={user} lastAdmin={user.role==='admin'&&user.enabled&&enabledAdmins===1} onAction={action}/>)}</ul></section>
    {request?<ReauthDialog key={request.key} request={request} csrf={csrf} requireTOTP={policy?.data.enabled||request.target==='totp'} onCancel={()=>setRequest(null)} onComplete={complete}/>:null}
  </div>;
}
