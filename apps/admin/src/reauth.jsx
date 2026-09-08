// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {reauthenticate,executeAction} from './security-api.js';

export default function ReauthDialog({request,csrf,requireTOTP,onComplete,onCancel}) {
  const dialog=useRef(null),locked=useRef(false),proof=useRef(null);
  const [busy,setBusy]=useState(false),[error,setError]=useState(''),[retry,setRetry]=useState(false);
  useEffect(()=>{const node=dialog.current,previous=document.activeElement;node.showModal();return()=>{node.close();previous?.focus();};},[]);
  useEffect(()=>{if(retry)dialog.current?.querySelector('button')?.focus();},[retry]);
  function keepFocus(event){
    if(event.key!=='Tab')return;
    const fields=[...dialog.current.querySelectorAll('button,input,select,textarea,a[href],[tabindex]')].filter(node=>!node.matches(':disabled')&&node.tabIndex>=0&&node.getClientRects().length);
    const first=fields[0],last=fields.at(-1);
    if(!first){event.preventDefault();dialog.current.focus();}
    else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}
  }
  async function submit(event){
    event.preventDefault();if(locked.current)return;locked.current=true;setBusy(true);setError('');
    const form=event.currentTarget,fields=new FormData(form);form.reset();
    try{
      if(!proof.current)proof.current=await reauthenticate(request,csrf,String(fields.get('password')),String(fields.get('totp')??''));
      const result=await executeAction(request,csrf,proof.current);proof.current=null;await onComplete(result);
    }catch(e){setError(e.message);if(e.status===503&&proof.current){setRetry(true);}else{proof.current=null;setRetry(false);}}
    finally{locked.current=false;setBusy(false);}
  }
  return <dialog ref={dialog} className="reauth-dialog" tabIndex={-1} onKeyDown={keepFocus} aria-labelledby="reauth-title" onCancel={event=>{event.preventDefault();if(!locked.current)onCancel();}}>
    <h2 id="reauth-title">재인증 후 실행</h2><p>{request.label}</p><p className="control-help">대상: {request.target} · {request.etag||'신규 생성'}. 확인 중에는 요청 내용이 바뀌지 않습니다.</p>
    <form onSubmit={submit}><fieldset disabled={busy}><legend>현재 Admin 확인</legend>{!retry?<>
      <label className="control-field"><span>현재 비밀번호</span><input name="password" type="password" autoComplete="current-password" required minLength={1} maxLength={1024} autoFocus/></label>
      {requireTOTP?<label className="control-field"><span>현재 인증 코드</span><input name="totp" inputMode="numeric" autoComplete="one-time-code" required pattern="[0-9]{6}" maxLength={6}/></label>:null}
      {requireTOTP?<p className="control-help">이미 로그인에 사용한 TOTP 코드는 재사용할 수 없습니다. 인증 앱의 다음 코드를 기다려 주세요.</p>:null}
    </>:<p role="status">응답을 확인하지 못했습니다. 같은 명령과 재시도 키로 결과를 다시 확인합니다.</p>}
    {error?<p role="alert" className="error">{error}</p>:null}<div className="control-actions"><button className="primary">{busy?'확인 중…':retry?'같은 명령 다시 확인':'확인하고 실행'}</button><button type="button" className="secondary" onClick={onCancel}>취소</button></div></fieldset></form>
  </dialog>;
}
