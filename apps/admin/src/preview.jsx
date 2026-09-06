// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {previewRequest} from './preview-api.js';
import {downloadReport} from './preview-download.js';
import './preview.css';

const initialPlan={schemaVersion:1,profile:'standard-10k',regionId:'ap-northeast-2',queuePolicy:'fifo',expectedPeakVisitors:10000,limits:{maxActiveAdmissionLeases:1000,admissionsPerMinute:600,admissionTtlSeconds:900},totp:{mode:'configurable',enabled:true}};
const initialPrice={schemaVersion:1,provider:'',currency:'USD',asOf:'',fractionDigits:2,monthlyHours:720,standardHostHourly:'',highWorkerHourly:'',volumeGiBMonthly:'',highWorkerVolumeGiB:100,expectedEgressGiB:0};
const priceFields=[['provider','가격 제공자 (소문자 식별자)','text'],['currency','통화 코드','text'],['asOf','단가 기준일','date'],['fractionDigits','표시 소수 자릿수','number',0,4],['monthlyHours','월 컴퓨팅 시간','number',1,744],['standardHostHourly','Standard 서버 시간당 단가','text'],['highWorkerHourly','High 워커 1대 시간당 단가','text'],['volumeGiBMonthly','디스크 GiB당 월 단가','text'],['highWorkerVolumeGiB','High 워커 1대 디스크 (GiB)','number',1,65536],['expectedEgressGiB','예상 egress (GiB, 비용 제외)','number',0,1000000000]];
function Field({label,...props}){return <label className="preview-field"><span>{label}</span><input required {...props}/></label>;}
function Raw({value}){return <details><summary>전체 JSON · 입력/산식/제외 항목</summary><pre>{JSON.stringify(value,null,2)}</pre></details>;}

export default function Preview(){
  const [step,setStep]=useState(0),[draft,setDraft]=useState(initialPlan),[price,setPrice]=useState(initialPrice),[plan,setPlan]=useState(null),[cost,setCost]=useState(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
  const [downloadNote,setDownloadNote]=useState('');
  const lock=useRef(false),heading=useRef(null);
  useEffect(()=>{document.title='Waiting Room · 설치 계획 미리보기';heading.current?.focus();},[step]);
  const high=draft.profile==='high-scale-100k',cap=high?100000:10000,rate=high?60000:6000;
  function edit(next){setDraft(next);setPlan(null);setCost(null);setError('');setDownloadNote('');}
  function editPrice(key,value){setPrice(p=>({...p,[key]:value}));setCost(null);setError('');setDownloadNote('');}
  async function exportReport(withCost){
    if(lock.current||!plan||(withCost&&!cost))return;
    lock.current=true;setBusy(true);setError('');setDownloadNote('');
    try{const report=await previewRequest('report',{schemaVersion:1,plan:plan.input,...(withCost?{cost:cost.input}:{})});downloadReport(report);setDownloadNote('JSON 다운로드를 요청했습니다. 파일은 계획 기록이며 설치 승인이나 백업이 아닙니다.');}
    catch(e){setError(e.message||'보고서를 내보내지 못했습니다. 다시 시도하세요.');}
    finally{lock.current=false;setBusy(false);}
  }
  async function submit(kind){if(lock.current)return;lock.current=true;setBusy(true);setError('');try{
    const out=await previewRequest(kind,kind==='plan'?draft:{...price,regionId:draft.regionId});
    if(kind==='plan'){setPlan(out);setStep(1);}else{setCost(out);}
  }catch(e){setError(e.message);}finally{lock.current=false;setBusy(false);}}
  return <div className="preview-shell"><header><span className="wordmark">Waiting Room</span><span>로컬 미리보기</span></header>
    <main className="preview-main"><aside><p className="preview-kicker">INSTALLATION PLANNER</p><h1>규모에 맞게,<br/>시작은 간단하게.</h1><p>필요한 용량과 보안 정책을 고르고<br/>설치 계획과 비용을 먼저 확인하세요.</p>
      <ol className="preview-steps">{['프로필과 보안','계획 확인','비용 비교'].map((label,i)=><li key={label} aria-current={step===i?'step':undefined}><span>{i+1}</span>{label}</li>)}</ol>
      <p className="preview-notice">계획 전용 · 설치되지 않습니다<br/>입력은 서버에 저장하지 않으며 새로고침하면 초기화됩니다. JSON은 버튼을 눌러 직접 보관할 수 있습니다. 비밀값을 입력하지 마세요.</p>
    </aside><section className="preview-panel"><h2 ref={heading} tabIndex={-1}>{['프로필과 보안','계획 확인','비용 비교'][step]}</h2>
      <p className="preview-note">10K/100K는 방문 상태 수의 한도입니다. 실제 동시 접속·HA·성능 인증을 뜻하지 않습니다.</p>
      {step===0?<form onSubmit={e=>{e.preventDefault();void submit('plan');}}><fieldset disabled={busy}>
        <label className="preview-field"><span>설치 프로필</span><select value={draft.profile} onChange={e=>edit({...draft,profile:e.target.value})}><option value="standard-10k">Standard 10K · 단일 서버 / Compose</option><option value="high-scale-100k">High Scale 100K · Kubernetes / 외부 HA 저장소</option></select></label>
        <p className="preview-note">{high?'워커 3대와 외부 PostgreSQL·Valkey를 직접 준비해야 합니다.':'4 vCPU · 8 GiB · SSD 50 GiB 단일 서버 기준. HA는 보장하지 않습니다.'}</p>
        <Field label="Home Region" value={draft.regionId} maxLength={63} pattern="[a-z][a-z0-9\-]*" onChange={e=>edit({...draft,regionId:e.target.value})}/>
        <div className="preview-grid"><Field label="예상 방문 상태 수" type="number" min={1} max={cap} value={draft.expectedPeakVisitors} onChange={e=>edit({...draft,expectedPeakVisitors:e.target.value===''?'':Number(e.target.value)})}/>
          <Field label="최대 활성 입장권 수" type="number" min={1} max={cap} value={draft.limits.maxActiveAdmissionLeases} onChange={e=>edit({...draft,limits:{...draft.limits,maxActiveAdmissionLeases:e.target.value===''?'':Number(e.target.value)}})}/>
          <Field label="분당 신규 입장 수" type="number" min={1} max={rate} value={draft.limits.admissionsPerMinute} onChange={e=>edit({...draft,limits:{...draft.limits,admissionsPerMinute:e.target.value===''?'':Number(e.target.value)}})}/>
          <Field label="입장권 유효 시간 (초)" type="number" min={60} max={3600} value={draft.limits.admissionTtlSeconds} onChange={e=>edit({...draft,limits:{...draft.limits,admissionTtlSeconds:e.target.value===''?'':Number(e.target.value)}})}/></div>
        <p className="preview-note">한도 {cap.toLocaleString('en-US')}명 · 분당 최대 {rate.toLocaleString('en-US')}명 · 알고리즘 FIFO</p>
        <label className="preview-field"><span>TOTP 정책</span><select value={draft.totp.mode} onChange={e=>edit({...draft,totp:{mode:e.target.value,enabled:e.target.value==='forced_on'?true:draft.totp.enabled}})}><option value="configurable">운영자가 ON/OFF 선택</option><option value="forced_on">배포 정책으로 ON 강제</option></select></label>
        <label className="preview-check"><input type="checkbox" checked={draft.totp.enabled} disabled={draft.totp.mode==='forced_on'} onChange={e=>edit({...draft,totp:{...draft.totp,enabled:e.target.checked}})}/>TOTP 사용</label>
        <p className="preview-note">{draft.totp.mode==='forced_on'?'강제 ON 정책에서는 OFF로 바꿀 수 없습니다.':'이 선택은 계획에만 반영되며 현재 관리자 보안 정책을 변경하지 않습니다.'}</p>
        <button className="primary" type="submit">{busy?'검증 중…':'계획 생성'}</button>
      </fieldset></form>:null}
      {step===1&&plan?<><div className="preview-result" role="status"><strong>입력 검증 완료 · 설치 미실행</strong><dl><dt>프로필</dt><dd>{plan.input.profile}</dd><dt>Home Region</dt><dd>{plan.input.regionId}</dd><dt>TOTP</dt><dd>{plan.input.totp.enabled?'ON':'OFF'} / {plan.input.totp.mode}</dd><dt>방문 상태 한도</dt><dd>{plan.visitorStateCap.toLocaleString('en-US')}</dd></dl></div>
        <h3>운영자가 준비할 항목</h3><ul>{plan.operatorProvides.map(x=><li key={x}>{x}</li>)}</ul>
        <details><summary>기준 자원 구성</summary><ul>{plan.referenceResources.map(x=><li key={x.name}>{x.name}: {x.replicas}개 · {x.cpuMillicoresPerReplica}m CPU · {x.memoryMiBPerReplica} MiB · {x.ownership}</li>)}</ul></details>
        <p className="preview-notice">환경 검사·서명 설정·origin 보호·Quick20·규모 인증은 미실행입니다. 실제 설치 및 활성화 기능은 아직 제공하지 않습니다.</p><Raw value={plan}/>
        <button className="primary" disabled={busy} onClick={()=>{setError('');setDownloadNote('');setStep(2);}}>비용 비교로 이동</button><button className="text-button" disabled={busy} onClick={()=>exportReport(false)}>계획 JSON 다운로드</button><button className="text-button" disabled={busy} onClick={()=>{setDownloadNote('');setStep(0);}}>설정 수정</button></>:null}
      {step===2?<><p className="preview-notice">서버 + 디스크 소계만 비교합니다. 외부 HA 저장소·CDN/LB·egress·백업·세금 등은 제외됩니다. 아래 단가를 직접 입력하세요. 실제 provider 견적을 조회하지 않습니다.</p>
        <p>가격 적용 리전: <strong>{draft.regionId}</strong></p>
        <form onSubmit={e=>{e.preventDefault();void submit('estimate');}}><fieldset disabled={busy}><div className="preview-grid">{priceFields.map(([key,label,type,min,max])=><Field key={key} label={label} type={type} min={min} max={max} value={price[key]} maxLength={key==='currency'?3:63} onChange={e=>editPrice(key,type==='number'&&e.target.value!==''?Number(e.target.value):e.target.value)}/>)}</div><button type="submit" className="primary">{busy?'계산 중…':'소계 계산'}</button></fieldset></form>
        {cost?<section className="preview-result" aria-label="비용 계산 결과" role="status"><h3>사용자 단가 기준 소계</h3><p>{cost.input.provider} · {cost.input.regionId} · {cost.input.asOf} · {cost.input.monthlyHours}시간</p><div className="preview-grid">{cost.profiles.map(p=><div key={p.profile}><h4>{p.profile}</h4><strong className="preview-amount">{p.roundedSubtotal} {cost.input.currency}</strong><p>서버 {p.hosts}대 · 대당 {p.volumeGiBPerHost}GiB</p></div>)}</div><p>포함 소계 배율: {cost.highToStandardRatio===null?'계산 불가 (Standard 소계 0)':cost.highToStandardRatio+'배'}</p><p>반올림 전 금액을 합산한 뒤 소수 {cost.input.fractionDigits}자리에서 half-up 반올림합니다. 총 운영비가 아닙니다.</p><Raw value={cost}/></section>:null}
        {cost?<button className="primary" disabled={busy} onClick={()=>exportReport(true)}>계획·비용 JSON 다운로드</button>:null}
        <button className="text-button" disabled={busy} onClick={()=>{setError('');setDownloadNote('');setStep(1);}}>계획으로 돌아가기</button></>:null}
      {downloadNote?<p className="preview-note" role="status">{downloadNote}</p>:null}
      {error?<p className="error" role="alert">{error}</p>:null}
    </section></main><footer><span>Apache-2.0 · Self-hosted</span><span>계획 전용 / 서버 저장 없음 / 실제 설치 없음</span></footer></div>;
}
