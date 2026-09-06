// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';

const decisions={protected:'경로상 보호 대상',excluded:'제외 경로',unprotected:'일치하는 활성 보호 경로 없음',reserved:'내부 전용 경로',invalid:'지원하지 않는 URL 형식'};
const reasons={protect_prefix:'보호 경로의 세그먼트가 일치합니다.',exclude_prefix:'제외 경로가 보호 경로보다 우선합니다.',no_active_rule:'호스트·경로 또는 Room 활성화 설정이 일치하지 않습니다.',internal_namespace:'Waiting Room 내부 namespace입니다.',noncanonical_url:'소문자 HTTPS 호스트와 정규 경로를 사용하세요. 인증정보·fragment·인코딩 경로·상대 경로는 허용하지 않습니다.'};
export default function RouteCheck({csrf,canRead,roomId,draftRevision,publishedRevision,generation}){
 const [busy,setBusy]=useState(false),[result,setResult]=useState(null),[error,setError]=useState('');
 const mounted=useRef(true),lock=useRef(false);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
 async function check(e){e.preventDefault();if(lock.current)return;const f=new FormData(e.currentTarget);lock.current=true;setBusy(true);setResult(null);setError('');try{const out=await controlAPI('/config/route-check',{method:'POST',csrf,body:{source:f.get('source'),url:f.get('url')}});if(mounted.current)setResult(out.data);}catch(e){if(mounted.current)setError(e.message);}finally{lock.current=false;if(mounted.current)setBusy(false);}}
 const stale=result&&(result.source==='draft'?result.revision!==draftRevision:result.revision!==publishedRevision||result.generation!==generation);
 return <section className="control-card"><h3>URL 경로 판정</h3><p className="control-help">저장 초안 또는 배포 스냅샷의 호스트·경로 규칙만 검사합니다. DNS·원본에 접속하지 않으며 입력 URL을 결과나 감사 로그에 저장하지 않습니다.</p>
 <form onSubmit={check} onChange={()=>{setResult(null);setError('');}}><fieldset disabled={busy||!canRead||!csrf}><legend>검사할 URL</legend><div className="control-grid"><label className="control-field"><span>검사 기준</span><select name="source" defaultValue="published"><option value="published">배포 설정</option><option value="draft">저장 초안</option></select></label><label className="control-field"><span>전체 HTTPS URL</span><input name="url" type="url" required maxLength={4096} autoComplete="off" spellCheck={false} placeholder="https://shop.example.com/shop/cart"/></label></div><button className="primary">{busy?'판정 중…':'URL 판정'}</button></fieldset></form>
 {!csrf?<p className="control-warning">이 탭에 요청 검증 정보가 없습니다. 로그인한 탭에서 검사하세요.</p>:null}
 {error?<p role="alert" className="error">{error}</p>:null}
 {result?<div role="status" className="route-result"><h4>{decisions[result.match.decision]??'판정 확인 필요'}</h4><p>{reasons[result.match.reason]??'서버 결과를 확인하세요.'}</p><p>{result.source==='draft'?'저장 초안':'배포 설정'} · revision {result.revision}{result.source==='published'?` · generation ${result.generation}`:''}</p>{result.match.roomId?<p>일치 Room: {result.match.roomId}{result.match.roomId!==roomId?' · 현재 화면과 다른 Room입니다.':''}</p>:null}{result.mode?<p>스냅샷 운영 모드: {result.mode}</p>:null}{stale?<p className="control-warning">화면의 설정 버전과 검사 결과가 다릅니다. 최신 설정을 불러온 뒤 다시 판정하세요.</p>:null}</div>:null}
 <p className="control-warning">경로상 보호 대상이어도 OFF·적용 대기·장애 상태에 따라 동작이 다릅니다. HTTP 응답이나 실제 입장을 보장하지 않습니다. 포트/TLS/원본 연결은 별도 확인이 필요합니다. 저장하지 않은 편집값은 검사에 포함되지 않습니다.</p></section>;
}
