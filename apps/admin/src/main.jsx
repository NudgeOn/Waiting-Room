// SPDX-License-Identifier: Apache-2.0
import React,{lazy,Suspense,useEffect,useRef,useState,useSyncExternalStore} from 'react';
import {createRoot} from 'react-dom/client';
import {api,readCSRF,saveCSRF} from './api.js';
import {Login,Factor,Enrollment,RecoveryCodes,Session} from './forms.jsx';
import Control from './control.jsx';
import Dashboard from './dashboard.jsx';
const SetupWizard=lazy(()=>import('./setup.jsx'));
const TrafficLab=lazy(()=>import('./traffic-lab.jsx'));
import {Link,RoomTabs} from './navigation.jsx';
import {navigate,parseRoute,routeSnapshot,subscribeRoute} from './routes.js';
import './style.css';

function App(){
  const path=useSyncExternalStore(subscribeRoute,routeSnapshot),route=parseRoute(path);
  const [phase,setPhase]=useState('loading'),[info,setInfo]=useState({}),[session,setSession]=useState(null),[pending,setPending]=useState(null),[account,setAccount]=useState(''),[codes,setCodes]=useState([]),[csrf,setCSRF]=useState(readCSRF),[busy,setBusy]=useState(false),[error,setError]=useState('');
  const lock=useRef(false),heading=useRef(null),logoutPending=useRef(null);
  useEffect(()=>{let live=true;Promise.all([fetch('/lab-info',{cache:'no-store',redirect:'error'}).then(r=>{if(!r.ok)throw Error();return r.json();}),api('/auth/me',{method:'GET'}).catch(e=>{if(e.status===401)return null;throw e;})]).then(async([config,current])=>{if(!current){try{await api('/auth/logout');}catch(e){if(e.status!==401)throw e;}}if(!live)return;setInfo(config);if(current){setSession(current);setPhase('session');}else{saveCSRF('');setCSRF('');setPhase(location.pathname==='/setup'&&config.setup?'setup':'login');}}).catch(()=>{if(live){setError('로컬 서버에 연결할 수 없습니다. 서버 상태를 확인한 뒤 새로고침하세요.');setPhase('unavailable');}});return()=>{live=false;};},[]);
  useEffect(()=>{if(phase!=='loading')heading.current?.focus();},[phase]);
  async function perform(action){if(lock.current)return;lock.current=true;setBusy(true);setError('');try{await action();}catch(e){setError(e.message||'요청을 완료하지 못했습니다.');}finally{lock.current=false;setBusy(false);}}
  function accept(out,user){if(user)setAccount(user);if(out.state==='authenticated'){setPending(null);setSession(out.session);setCSRF(out.csrfToken);saveCSRF(out.csrfToken);setCodes(out.recoveryCodes??[]);setPhase(out.recoveryCodes?'codes':'session');}else if(['totp_required','enrollment_required'].includes(out.state)){setPending(out);setPhase(out.state==='totp_required'?'factor':'enrollment');}else{throw Error('서버 응답을 확인할 수 없습니다.');}}
  function back(){setPending(null);setCodes([]);setAccount('');setError('');setPhase('login');}
  async function logout(){if(logoutPending.current?.csrf!==csrf)logoutPending.current={csrf,key:crypto.randomUUID()};try{await api('/auth/logout',{csrf,key:logoutPending.current.key});}catch(e){if(e.status!==401)throw e;}logoutPending.current=null;saveCSRF('');setCSRF('');setSession(null);back();}
  async function syncSession(nextCSRF){saveCSRF(nextCSRF);setCSRF(nextCSRF);try{setSession(await api('/auth/me',{method:'GET'}));}catch(e){if(e.status!==401)throw e;saveCSRF('');setCSRF('');setSession(null);back();}}
  const sessionPanel=<Session session={session} csrf={csrf} perform={perform} logout={logout} openControl={info.draftAuthoring?()=>navigate('/rooms'):undefined}/>;
  if(phase==='session'&&info.draftAuthoring){
    const dashboard=['dashboard','login','setup','session'].includes(route.page);
    return <div className="shell control-shell"><a className="skip-link" href="#console-content">본문으로 건너뛰기</a><header><Link className="wordmark" to="/">Waiting Room</Link><span>관리자 콘솔</span></header><main id="console-content" tabIndex={-1}><h1 className="sr-only">Waiting Room 관리자 콘솔</h1>
      <nav className="workspace-nav" aria-label="콘솔 화면"><Link to="/" aria-current={dashboard?'page':undefined}>대시보드</Link><Link to="/rooms" aria-current={['room','rooms','new','runtime'].includes(route.page)?'page':undefined}>Rooms</Link>{info.trafficLab&&session.capabilities.some(c=>c.action==='lab.read')?<Link to="/traffic-lab" aria-current={route.page==='traffic-lab'?'page':undefined}>Traffic Lab</Link>:null}{info.persistent&&session.capabilities.some(c=>c.action==='security.read')?<Link to="/settings" aria-current={route.page==='security'?'page':undefined}>사용자 · 보안</Link>:null}</nav>
      {dashboard?<><Dashboard persistent={info.persistent===true} csrf={csrf} session={session} canWrite={Boolean(csrf)&&session.capabilities.some(c=>c.action==='config.write'&&!c.requiresReauthentication)}/><section className="control-card">{sessionPanel}{error?<p className="error" role="alert">{error}</p>:null}</section></>:route.page==='not-found'?<section><h2>화면을 찾을 수 없습니다.</h2><Link to="/">대시보드로 돌아가기</Link></section>:<>
        {route.page==='room'?<RoomTabs id={route.roomId} tab={route.tab}/>:null}
        {route.page==='traffic-lab'?(info.trafficLab?<Suspense fallback={<p role="status">Traffic Lab을 불러오는 중…</p>}><TrafficLab session={session} csrf={csrf}/></Suspense>:<p role="alert">Traffic Lab이 포함된 로컬 runtime이 필요합니다.</p>):<Control key={path} route={route} session={session} csrf={csrf} persistent={info.persistent===true} onEnrollment={out=>accept(out,session.userId)} onSessionChanged={syncSession} onBack={()=>navigate('/')}/>}
      </>}
    </main></div>;
  }
  return <div className={phase==='setup'&&info.setupWizard?'shell setup-shell':'shell'}><header><span className="wordmark">Waiting Room</span><span>관리자 콘솔</span></header><main><section className="welcome"><h1>흐름은 차분하게,<br/>운영은 간편하게.</h1><p>사이트와 앱의 대기열을<br/>한곳에서 안전하게 관리하세요.</p><small>Apache-2.0 · Self-hosted</small></section><section className="form-surface" ref={heading} tabIndex={-1} aria-label="관리자 인증">
    {phase==='loading'?<p role="status">서버 연결 확인 중…</p>:null}
    {phase==='setup'&&info.setupWizard?<Suspense fallback={<p role="status">설치 위자드 로딩 중…</p>}><SetupWizard busy={busy} perform={perform} accept={accept}/></Suspense>:phase==='login'||phase==='setup'?<Login key={phase} setup={phase==='setup'} info={{...info,busy}} perform={perform} accept={accept}/>:null}
    {phase==='factor'?<Factor pending={pending} perform={perform} accept={accept} back={back} busy={busy}/>:null}
    {phase==='enrollment'?<Enrollment pending={pending} account={account} perform={perform} accept={accept} back={back} busy={busy} csrf={csrf}/>:null}
    {phase==='codes'?<RecoveryCodes codes={codes} onDone={()=>{setCodes([]);setPhase('session');}}/>:null}
    {phase==='session'?<>{sessionPanel}{info.setupWizard?<><p className="notice">초기 설정이 완료되었습니다. 관리자 콘솔에서 다시 로그인하면 Room을 만들고 운영할 수 있습니다.</p><button className="secondary" disabled={busy} onClick={()=>perform(async()=>{await logout();location.assign(info.adminUrl+'/auth/login');})}>설정 완료 · 관리자 콘솔로 이동</button></>:null}</>:null}
    {error?<p className="error" role="alert">{error}</p>:null}
  </section></main><footer><span>Waiting Room</span><span>로그인 정보는 이 서버에서만 처리됩니다.</span></footer></div>;
}
const root=createRoot(document.getElementById('root'));
if(location.pathname==='/install-preview'){
  root.render(<p role="status">설치 계획 미리보기 로딩 중…</p>);
  import('./preview.jsx').then(({default:Preview})=>root.render(<Preview/>)).catch(()=>root.render(<p role="alert">화면을 불러오지 못했습니다. 새로고침하세요.</p>));
}else{root.render(<App/>);}
