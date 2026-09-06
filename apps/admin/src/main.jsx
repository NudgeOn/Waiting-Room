// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {api,readCSRF,saveCSRF} from './api.js';
import {Login,Factor,Enrollment,RecoveryCodes,Session} from './forms.jsx';
import Control from './control.jsx';
import './style.css';

function App(){
  const [phase,setPhase]=useState('loading'),[info,setInfo]=useState({}),[session,setSession]=useState(null),[pending,setPending]=useState(null),[account,setAccount]=useState(''),[codes,setCodes]=useState([]),[csrf,setCSRF]=useState(readCSRF),[busy,setBusy]=useState(false),[error,setError]=useState('');
  const lock=useRef(false),heading=useRef(null);
  useEffect(()=>{let live=true;Promise.all([fetch('/lab-info',{cache:'no-store',redirect:'error'}).then(r=>{if(!r.ok)throw Error();return r.json();}),api('/auth/me',{method:'GET'}).catch(e=>{if(e.status===401)return null;throw e;})]).then(async([config,current])=>{if(!current){try{await api('/auth/logout');}catch(e){if(e.status!==401)throw e;}}if(!live)return;setInfo(config);if(current){setSession(current);setPhase('session');}else{saveCSRF('');setCSRF('');setPhase(location.pathname==='/setup'&&config.setup?'setup':'login');}}).catch(()=>{if(live){setError('로컬 서버에 연결할 수 없습니다. 서버 상태를 확인한 뒤 새로고침하세요.');setPhase('unavailable');}});return()=>{live=false;};},[]);
  useEffect(()=>{if(phase!=='loading')heading.current?.focus();},[phase]);
  async function perform(action){if(lock.current)return;lock.current=true;setBusy(true);setError('');try{await action();}catch(e){setError(e.message||'요청을 완료하지 못했습니다.');}finally{lock.current=false;setBusy(false);}}
  function accept(out,user){if(user)setAccount(user);if(out.state==='authenticated'){setPending(null);setSession(out.session);setCSRF(out.csrfToken);saveCSRF(out.csrfToken);setCodes(out.recoveryCodes??[]);setPhase(out.recoveryCodes?'codes':'session');}else if(['totp_required','enrollment_required'].includes(out.state)){setPending(out);setPhase(out.state==='totp_required'?'factor':'enrollment');}else{throw Error('서버 응답을 확인할 수 없습니다.');}}
  function back(){setPending(null);setCodes([]);setAccount('');setError('');setPhase('login');}
  async function logout(){try{await api('/auth/logout',{csrf});}catch(e){if(e.status!==401)throw e;}saveCSRF('');setCSRF('');setSession(null);back();}
  if(phase==='control')return <div className="shell control-shell"><header><span className="wordmark">Waiting Room</span><span>관리자 콘솔</span></header><main><Control session={session} csrf={csrf} persistent={info.persistent===true} onBack={()=>setPhase('session')}/></main></div>;
  return <div className="shell"><header><span className="wordmark">Waiting Room</span><span>관리자 콘솔</span></header><main><section className="welcome"><h1>흐름은 차분하게,<br/>운영은 간편하게.</h1><p>사이트와 앱의 대기열을<br/>한곳에서 안전하게 관리하세요.</p><small>Apache-2.0 · Self-hosted</small></section><section className="form-surface" ref={heading} tabIndex={-1} aria-label="관리자 인증">
    {phase==='loading'?<p role="status">서버 연결 확인 중…</p>:null}
    {phase==='login'||phase==='setup'?<Login key={phase} setup={phase==='setup'} info={{...info,busy}} perform={perform} accept={accept}/>:null}
    {phase==='factor'?<Factor pending={pending} perform={perform} accept={accept} back={back} busy={busy}/>:null}
    {phase==='enrollment'?<Enrollment pending={pending} account={account} perform={perform} accept={accept} back={back} busy={busy}/>:null}
    {phase==='codes'?<RecoveryCodes codes={codes} onDone={()=>{setCodes([]);setPhase('session');}}/>:null}
    {phase==='session'?<Session session={session} csrf={csrf} perform={perform} logout={logout} openControl={info.draftAuthoring?()=>setPhase('control'):undefined}/>:null}
    {error?<p className="error" role="alert">{error}</p>:null}
  </section></main><footer><span>Waiting Room</span><span>로그인 정보는 이 서버에서만 처리됩니다.</span></footer></div>;
}
const root=createRoot(document.getElementById('root'));
if(location.pathname==='/install-preview'){
  root.render(<p role="status">설치 계획 미리보기 로딩 중…</p>);
  import('./preview.jsx').then(({default:Preview})=>root.render(<Preview/>)).catch(()=>root.render(<p role="alert">화면을 불러오지 못했습니다. 새로고침하세요.</p>));
}else{root.render(<App/>);}
