// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useState} from 'react';
import {api,provisioningURI,USER_PATTERN,RECOVERY_PATTERN} from './api.js';
import {controlAPI} from './control-api.js';

export function Field({label,name,type='text',...props}){return <label className="field"><span>{label}</span><input name={name} type={type} required {...props}/></label>;}
export function Submit({busy,children}){return <button className="primary" disabled={busy}>{busy?'처리 중…':children}</button>;}
export function Login({setup,info,perform,accept}) {
  return <><h2>{setup?'첫 관리자 만들기':'관리자 로그인'}</h2><p className="intro">{setup?(info.persistent?'이 Docker 설치의 첫 관리자를 만듭니다. 계정은 재시작 후에도 유지됩니다.':'격리된 로컬 실험입니다. 종료하면 계정이 삭제됩니다.'):'설치 시 만든 계정으로 로그인하세요.'}</p>
    <form onSubmit={e=>{e.preventDefault();const form=e.currentTarget;const values=new FormData(form);perform(async()=>{const out=await api(setup?'/bootstrap':'/auth/login',{body:{username:values.get('username'),password:values.get('password')},installToken:setup?values.get('installToken'):undefined});form.reset();accept(out,values.get('username'));});}}>
      {setup?<Field label="설치 토큰" name="installToken" type="password" autoComplete="off" minLength={43} maxLength={43}/>:null}
      <Field label="사용자 이름" name="username" placeholder="admin" autoComplete="username" pattern={USER_PATTERN} maxLength={128}/>
      <Field label="비밀번호" name="password" type="password" autoComplete={setup?'new-password':'current-password'} minLength={setup?15:1} maxLength={1024}/>
      <Submit busy={info.busy}>{setup?'관리자 만들기':'로그인'}</Submit>
    </form><p className="hint">{setup?`TOTP ${info.totpEnabled?'ON':'OFF'} · 서버의 초기 정책을 따릅니다. 15자 이상 비밀번호를 사용하세요.`:'TOTP가 켜져 있으면 다음 단계에서 인증합니다.'}</p></>;
}
export function Factor({pending,perform,accept,back,busy}){
  const [recovery,setRecovery]=useState(false);
  return <><h2>{recovery?'복구 코드 로그인':'인증 코드 확인'}</h2><p className="intro">{recovery?'보관한 미사용 복구 코드를 입력하세요.':'인증 앱에 표시된 6자리 코드를 입력하세요.'}</p><form key={recovery?'recovery':'otp'} onSubmit={e=>{e.preventDefault();const form=e.currentTarget;const code=new FormData(form).get('code');perform(async()=>{const out=await api(recovery?'/auth/totp/recover':'/auth/totp/verify',{proof:pending.challengeToken,body:recovery?{recoveryCode:code}:{code}});form.reset();accept(out);});}}>
    <Field label={recovery?'복구 코드':'인증 코드'} name="code" autoComplete={recovery?'off':'one-time-code'} inputMode={recovery?'text':'numeric'} pattern={recovery?RECOVERY_PATTERN:'[0-9]{6}'} maxLength={recovery?43:6}/><Submit busy={busy}>확인</Submit></form>
    <button className="text-button" disabled={busy} onClick={()=>setRecovery(v=>!v)}>{recovery?'인증 코드 사용':'복구 코드 사용'}</button><button className="text-button" disabled={busy} onClick={back}>로그인부터 다시 시작</button></>;
}
export function Enrollment({pending,account,perform,accept,back,busy,csrf}){
  async function enrollmentRequest(verify,body){if(pending.policyEnrollment){return (await controlAPI('/security/totp/enrollment/'+(verify?'verify':'start'),{method:'POST',csrf,body:{challengeToken:pending.challengeToken,...body}})).data;}return api('/auth/totp/enroll'+(verify?'/verify':''),{proof:pending.challengeToken,body});}
  const [setup,setSetup]=useState(null),[qr,setQR]=useState('');
  // Only CPU-local QR rendering is an effect; Begin/Complete remain user actions.
  useEffect(()=>{let live=true;if(setup){import('qrcode').then(m=>(m.default??m).toDataURL(provisioningURI(setup.secret,account),{width:216,margin:2})).then(url=>{if(live)setQR(url);}).catch(()=>{if(live)setQR('');});}return()=>{live=false;};},[setup,account]);
  return <><h2>TOTP 등록</h2><p className="intro">인증 앱에 등록한 뒤 6자리 코드로 확인하세요.</p>{!setup?<button className="primary" disabled={busy} onClick={()=>perform(async()=>setSetup(await enrollmentRequest(false,{})))}>등록 키 만들기</button>:<>
    {qr?<img className="qr" src={qr} width="216" height="216" alt="인증 앱에 등록할 QR 코드"/>:null}
    <label className="field"><span>수동 등록 키</span><input className="manual-key" readOnly value={setup.secret} aria-label="수동 등록 키" autoComplete="off"/></label>
    <p className="hint">키와 QR은 이 브라우저에서만 표시됩니다. 등록 토큰은 5분 후 만료됩니다.</p>
    <form onSubmit={e=>{e.preventDefault();const form=e.currentTarget;const code=new FormData(form).get('code');perform(async()=>{const out=await enrollmentRequest(true,{code});form.reset();setSetup(null);setQR('');accept(out);});}}><Field label="인증 코드" name="code" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} autoComplete="one-time-code"/><Submit busy={busy}>등록 완료</Submit></form>
  </>}<button className="text-button" disabled={busy} onClick={back}>로그인부터 다시 시작</button></>;
}
export function RecoveryCodes({codes,onDone}){
  const [saved,setSaved]=useState(false);
  return <><h2>복구 코드를 보관하세요</h2><p className="intro">다시 표시되지 않습니다. 각 코드는 한 번만 사용할 수 있습니다.</p><ol className="recovery-codes">{codes.map(code=><li key={code}><code>{code}</code></li>)}</ol><label className="check"><input type="checkbox" checked={saved} onChange={e=>setSaved(e.target.checked)}/>안전한 곳에 코드를 보관했습니다.</label><button className="primary" disabled={!saved} onClick={onDone}>계속</button></>;
}
export function Session({session,csrf,perform,logout,openControl}){
  return <><h2>관리자 세션</h2><p className="intro">서버에서 확인한 현재 인증 상태입니다.</p><dl className="session"><dt>사용자</dt><dd>{session.userId}</dd><dt>역할</dt><dd>{session.role}</dd><dt>MFA 인증</dt><dd>{session.mfaVerified?'완료':'TOTP OFF'}</dd><dt>세션 만료</dt><dd>{new Date(session.idleExpiresAt).toLocaleString('ko-KR')}</dd></dl>
    <p className="notice">Room 설정은 초안 저장 후 별도로 배포합니다. 운영 화면에서 Gateway와 Coordinator의 실제 적용 상태를 확인하세요.</p>
    {openControl?<button className="secondary" onClick={openControl}>Room 초안 관리</button>:null}
    <details><summary>계정 권한 확인</summary><ul className="capabilities">{session.capabilities.map(c=><li key={c.action}>{c.action}{c.requiresReauthentication?' · 재인증 필요':''}</li>)}</ul></details>
    {csrf?<button className="primary" onClick={()=>perform(logout)}>로그아웃</button>:<p className="notice">이 탭에는 로그아웃 검증 정보가 없습니다. 로그인한 탭에서 로그아웃하세요. 이미 만료됐다면 아래 버튼으로 쿠키를 정리할 수 있습니다.</p>}
    {!csrf?<button className="text-button" onClick={()=>perform(logout)}>만료 세션 정리</button>:null}</>;
}
