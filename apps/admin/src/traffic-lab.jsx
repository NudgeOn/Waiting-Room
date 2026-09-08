// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {controlAPI} from './control-api.js';
import {activeRun,downloadLab,labChecks,labStages,labStates,mergeRun,presetLabel} from './traffic-lab.js';
import './traffic-lab.css';

export default function TrafficLab({csrf,session,embedded=false}){
 const [items,setItems]=useState([]),[selected,setSelected]=useState(''),[loaded,setLoaded]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[refresh,setRefresh]=useState(0);
 const live=useRef(true),lock=useRef(false),pending=useRef(null),heading=useRef(null),mutation=useRef(0);
 const canRead=session.capabilities.some(c=>c.action==='lab.read'),canRun=Boolean(csrf)&&session.capabilities.some(c=>c.action==='lab.run');
 useEffect(()=>{live.current=true;return()=>{live.current=false;};},[]);
 useEffect(()=>{if(!embedded)heading.current?.focus();},[embedded]);
 useEffect(()=>{
  if(!canRead)return;let stopped=false,timer;
  async function poll(){const version=mutation.current;try{const out=await controlAPI('/lab/runs');if(stopped||version!==mutation.current)return;setItems(out.data.items);setLoaded(true);setError('');}catch(e){if(!stopped&&version===mutation.current){setError(e.message);setLoaded(false);}}finally{if(!stopped)timer=setTimeout(poll,1500);}}
  poll();return()=>{stopped=true;clearTimeout(timer);};
 },[canRead,refresh]);
 const current=items.find(item=>item.id===selected)??items[0],running=items.find(activeRun),report=current?.report;
 async function command(preset,cancel=false){
  if(lock.current||!canRun)return;lock.current=true;setBusy(true);setError('');setNotice('');mutation.current++;
  const path=cancel?`/lab/runs/${current.id}/cancel`:'/lab/runs',body=cancel?{}:{preset},signature=JSON.stringify({path,body});
  if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};
  try{const out=await controlAPI(path,{method:'POST',body,csrf,key:pending.current.key});pending.current=null;if(live.current){setItems(old=>mergeRun(old,out.data));setSelected(out.data.id);setNotice(cancel?'중지를 요청했습니다. 시험 환경 종료 후 결과가 저장됩니다.':'시험을 시작했습니다. 이 화면을 나가도 결과를 다시 확인할 수 있습니다.');}}
  catch(e){if(e.status&&e.status<500)pending.current=null;if(live.current)setError(e.message);}
  finally{lock.current=false;mutation.current++;if(live.current){setBusy(false);setRefresh(n=>n+1);}}
 }
 const Heading=embedded?'h3':'h2';
 return <section className="traffic-lab" aria-label="Traffic Lab">
  <div className="tl-title"><div><span className="tl-eyebrow">샘플 환경 · 기능 검사</span><Heading ref={heading} tabIndex={-1}>Traffic Lab</Heading><p>가상 방문자가 기다리고 입장하는 흐름을 확인하세요.</p></div><span className="tl-scope">운영 트래픽과 분리</span></div>
  <p className="tl-explainer">전용 샘플 상점에서 실행합니다. 현재 Room의 설정·유량과 고객 원본에는 시험 요청을 보내지 않습니다. 10K·100K 처리 성능 인증은 별도입니다.</p>
  {!canRead?<p role="alert">시험 결과 조회 권한이 없습니다.</p>:<>
   {error?<div className="error" role="alert">{error}<button type="button" className="text-button" onClick={()=>setRefresh(n=>n+1)}>상태 다시 확인</button></div>:null}
   {notice?<p role="status" className="tl-notice">{notice}</p>:null}
   <div className="tl-presets">{[['quick-20','20명 · 브라우저 + 앱','방문자별 순서와 대기·입장 결과를 확인합니다.'],['smoke-1k','1,000명 · 앱 HTTP','동시 요청과 중복 재시도에서 FIFO·입장 한도를 확인합니다.']].map(([preset,subtitle,description])=><article key={preset}><span className="tl-count">{subtitle}</span><h3>{presetLabel(preset)}</h3><p>{description}</p><button type="button" className="primary" disabled={!loaded||!canRun||busy||Boolean(running)} onClick={()=>command(preset)}>{presetLabel(preset)} 실행</button></article>)}</div>
   {!canRun?<p className="control-help">Admin·Operator가 로그인한 탭에서 실행할 수 있습니다. 현재 계정에서는 저장된 결과를 확인하세요.</p>:null}
   {running?<p className="tl-running" role="status">{presetLabel(running.preset)} · {labStates[running.state]}. 한 번에 하나의 시험을 실행합니다.</p>:null}
   {!loaded&&!error?<p role="status">최근 실행 결과를 불러오는 중…</p>:null}
   {loaded&&items.length===0?<div className="tl-empty"><strong>첫 시험을 실행해 보세요</strong><p>Quick 20으로 흐름을 살펴본 뒤 Smoke 1K로 동시 요청을 확인하세요.</p></div>:null}
   {current?<div className="tl-result">
    <div className="tl-result-title"><div><span className={'tl-status tl-'+current.state}>{labStates[current.state]}</span><h3>{presetLabel(current.preset)} 결과</h3><p>{new Date(current.createdAt).toLocaleString('ko-KR')} · {current.actorId}</p></div><div className="tl-actions">{canRun&&activeRun(current)?<button className="secondary" disabled={busy||current.state==='cancelling'} onClick={()=>command(current.preset,true)}>시험 중지</button>:null}{report&&!activeRun(current)?<button className="secondary" onClick={()=>downloadLab(current)}>결과 JSON 다운로드</button>:null}</div></div>
    {activeRun(current)?<div className="tl-progress"><label htmlFor="lab-progress">{current.state==='cancelling'?'시험 환경 종료 중':labStages[report?.stage]??'실행기 연결 대기'} · {report?.joined??0}/{current.preset==='quick-20'?20:1000}명</label><progress id="lab-progress" value={report?.joined??0} max={current.preset==='quick-20'?20:1000}/><p>최대 90초 안에 종료합니다. 진행 상태는 자동으로 갱신됩니다.</p></div>:null}
    {['interrupted','failed','cancelled'].includes(current.state)?<p className="control-warning">{current.state==='interrupted'?'실행기 연결이나 서버 응답이 끊겼습니다. 확인된 단계까지만 표시합니다.':current.state==='cancelled'?'시험을 중지했습니다. 아래 값은 완료한 부분의 결과입니다.':'기대 결과와 다른 동작이 확인됐습니다. 아래 판정과 요청 오류를 확인하세요.'} 전체 통과로 판정하지 않습니다.</p>:null}
    {report?<><dl className="tl-metrics">{[['생성 방문자',report.joined.toLocaleString('ko-KR')],['입장 완료',report.admitted],['대기',report.queued],['예상 밖 오류',report.unexpectedErrors],['실행 시간',`${(report.durationMs/1000).toFixed(1)}초`],['요청 p95',`${report.p95Ms}ms`]].map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl><p className="control-help">총 {report.requests.toLocaleString('ko-KR')}회 요청 · 의도한 차단 {report.expectedRejections}회. p95는 이 기능 시험의 응답 시간이며 처리 용량을 보장하지 않습니다.</p>
     <h4>판정 근거</h4>{report.checks.length?<ul className="tl-checks">{report.checks.map((check,i)=><li key={i}><span className={check.passed?'tl-pass':'tl-fail'}>{check.passed?'통과':'실패'}</span><div><strong>{labChecks[check.name]??'흐름 검사'}</strong><span>기대: {check.expected}</span><span>실제: {check.actual}</span></div></li>)}</ul>:<p>아직 완료된 검사가 없습니다.</p>}
     {report.timeline.length?<><h4>방문자 타임라인 {current.preset==='smoke-1k'?'· 첫 20명':''}</h4><p className="control-help">실제 대기열 순번 기준입니다. 입장권·쿠키·비밀값은 기록하지 않습니다.</p><div className="tl-timeline" role="region" aria-label="방문자별 기대 결과와 실제 결과" tabIndex={0}><table><thead><tr><th>순번</th><th>방문자</th><th>클라이언트</th><th>기대</th><th>실제</th></tr></thead><tbody>{report.timeline.map(event=><tr key={event.sequence}><td>{event.sequence}</td><td>{event.visitor}</td><td>{event.client==='browser'?'브라우저':'앱'}</td><td>{event.expected==='admitted'?'입장':'대기'}</td><td>{event.actual==='admitted'?'입장':'대기'} · {event.passed?'일치':'불일치'}</td></tr>)}</tbody></table></div></>:null}
    </>:null}
   </div>:null}
   {items.length?<section className="tl-history"><h3>최근 실행</h3><p className="control-help">최근 20건을 표시합니다. 서버에는 최대 500건을 보관하며 새 실행 시 24시간 지난 종료 기록을 정리합니다.</p><ul>{items.map(run=><li key={run.id}><button type="button" aria-pressed={current?.id===run.id} onClick={()=>setSelected(run.id)}><strong>{presetLabel(run.preset)}</strong><time dateTime={run.createdAt}>{new Date(run.createdAt).toLocaleString('ko-KR')}</time><span>{labStates[run.state]}</span></button></li>)}</ul></section>:null}
  </>}
 </section>;
}
