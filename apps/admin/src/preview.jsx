// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useId,useRef,useState} from 'react';
import {previewRequest} from './preview-api.js';
import {downloadReport} from './preview-download.js';
import {initialPlan,planSteps,validatePlanStep,validatePrice} from './preview-inputs.js';
import './preview.css';

const initialPrice={schemaVersion:1,provider:'',currency:'USD',asOf:'',fractionDigits:2,monthlyHours:720,standardHostHourly:'',highWorkerHourly:'',volumeGiBMonthly:'',highWorkerVolumeGiB:100,expectedEgressGiB:0};
const stepDescriptions=[
  '운영할 서버 구성에 맞는 프로필을 선택하세요. 필요한 자원과 직접 준비할 항목을 함께 안내합니다.',
  '대기열 상태를 처리할 리전을 정하고, 설치 전에 준비해야 할 환경을 확인하세요.',
  '예상 규모와 원본 서비스가 감당할 입장 속도를 정하세요. 기본값에서 시작해 조정할 수 있습니다.',
  '관리자 로그인을 보호할 인증 정책을 정하세요. 여기서는 계정이나 인증 키를 만들지 않습니다.',
  '선택한 설정과 서버 구성을 검토하세요. 수정할 항목은 해당 단계로 바로 돌아갈 수 있습니다.',
  '사용할 서버의 단가로 월 소계를 비교하세요. 비용 입력은 선택 사항이며 계획만 내려받아도 됩니다.',
];
const environmentItems={
  standard:[['서버와 실행 환경','Linux 서버 1대 · 4 vCPU / 8 GiB RAM / 50 GiB SSD, Docker Engine을 준비하세요.'],['도메인과 HTTPS','서비스·관리자 도메인, DNS, TLS 종료 지점과 사용할 포트를 준비하세요.'],['저장소와 시간 동기화','영속 디스크와 백업 계획, NTP/chrony 시간 동기화를 확인하세요. PostgreSQL·Valkey는 단일 호스트 구성입니다.'],['원본 서버 보호','방문자가 Gateway를 우회하지 못하도록 원본 ACL 또는 mTLS를 준비하세요.']],
  high:[['클러스터와 장애 영역','Kubernetes 워커 3대 · 대당 8 vCPU / 16 GiB RAM, 장애 영역 3개를 준비하세요.'],['외부 데이터 저장소','HA PostgreSQL과 Valkey, TLS·인증·백업·복구 목표를 직접 준비하세요. 앱 설치 범위에 포함되지 않습니다.'],['네트워크와 접근 정책','Ingress/LB, 서비스·관리자 DNS/TLS, NetworkPolicy와 원본 ACL/mTLS가 필요합니다.'],['행사 전 사전 확장','10분 전 Gateway 8개·Coordinator 4개, 5분 전 Ready 상태와 저장소 응답 지연을 확인해야 합니다.']],
};
const checkLabels={
  'environment-capabilities':'실행 환경과 의존성', 'dns-tls-ports-storage':'DNS · TLS · 포트 · 영속 디스크',
  'clock-source-and-offset':'시간 동기화와 시계 오차', 'secret-mounts':'비밀값 마운트', 'signed-config':'서명된 설정 적용',
  'origin-protection-active-probe':'원본 우회 방지', 'quick-20':'Quick 20 대기·입장 테스트', 'profile-qualification':'프로필 규모 검증',
  'external-store-tls-auth-role-read-write-switch':'외부 저장소 연결·인증·역할 전환', 'operator-HA-backup-RPO-RTO-record':'HA · 백업 · 복구 목표 기록',
  'three-domain-topology-and-sentinel-quorum-2':'장애 영역 3개와 Sentinel quorum', 'hpa-pdb-and-network-policy':'자동 확장·중단 예산·네트워크 정책',
  'T-minus-10m-gateway-8-coordinator-4':'행사 10분 전 사전 확장', 'T-minus-5m-all-ready-and-valkey-p95-RTT-less-than-2ms':'행사 5분 전 Ready · 저장소 지연',
};
const priceFields=[
  ['provider','가격 제공자 (소문자 식별자)','text',null,null,'예: my-cloud · 소문자, 숫자, 하이픈'],
  ['currency','통화 코드','text',null,null,'예: USD, KRW · 대문자 3자리'],
  ['asOf','단가 기준일','date','2000-01-01','9999-12-31','확인한 단가의 기준 날짜'],
  ['monthlyHours','월 컴퓨팅 시간','number',1,744,'기본 720시간 · 30일 × 24시간'],
  ['standardHostHourly','Standard 서버 시간당 단가','text',null,null,'4 vCPU / 8 GiB 서버 1대 · 디스크 제외'],
  ['highWorkerHourly','High 워커 1대 시간당 단가','text',null,null,'8 vCPU / 16 GiB 워커 1대 · 디스크 제외'],
  ['volumeGiBMonthly','디스크 GiB당 월 단가','text',null,null,'쉼표 없이 입력 · 소수점 이하 최대 6자리'],
  ['highWorkerVolumeGiB','High 워커 1대 디스크 (GiB)','number',1,65536,'비용 산정용 용량 · 기본 100 GiB'],
  ['fractionDigits','표시 소수 자릿수','number',0,4,'총 소계를 합산한 뒤 반올림'],
  ['expectedEgressGiB','예상 egress (GiB, 비용 제외)','number',0,1000000000,'예상 전송량 기록용 · 소계에 미포함'],
];
const format=value=>Number(value).toLocaleString('ko-KR');

function Field({label,hint,error,...props}){
  const id=useId();
  return <div className="preview-field"><label htmlFor={id}>{label}</label><input id={id} required {...props} aria-invalid={error?true:undefined} aria-describedby={error?`${id}-error`:hint?`${id}-hint`:undefined}/>{error?<span id={`${id}-error`} className="preview-field-error">{error}</span>:hint?<small id={`${id}-hint`}>{hint}</small>:null}</div>;
}
function Raw({value}){return <details className="preview-raw"><summary>전체 JSON · 입력/산식/제외 항목</summary><pre>{JSON.stringify(value,null,2)}</pre></details>;}
function ReviewSection({title,editLabel,onEdit,children,busy}){return <section className="preview-review-section"><div className="preview-section-heading"><h3>{title}</h3><button type="button" className="preview-link" disabled={busy} onClick={onEdit}>{editLabel}</button></div>{children}</section>;}

export default function Preview(){
  const [step,setStep]=useState(0),[furthest,setFurthest]=useState(0),[draft,setDraft]=useState(initialPlan),[price,setPrice]=useState(initialPrice);
  const [plan,setPlan]=useState(null),[cost,setCost]=useState(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[fieldErrors,setFieldErrors]=useState({}),[downloadNote,setDownloadNote]=useState('');
  const lock=useRef(false),heading=useRef(null),form=useRef(null);
  useEffect(()=>{document.title='Waiting Room · 설치 계획 미리보기';heading.current?.focus();},[step]);
  const high=draft.profile==='high-scale-100k',cap=high?100000:10000,rate=high?60000:6000;
  function move(next){setStep(next);setError('');setFieldErrors({});setDownloadNote('');}
  function edit(next){setDraft(next);setPlan(null);setCost(null);setFurthest(current=>Math.min(current,3));setFieldErrors({});setError('');setDownloadNote('');}
  function editPrice(key,value){setPrice(p=>({...p,[key]:value}));setCost(null);setFieldErrors({});setError('');setDownloadNote('');}
  function showErrors(errors){setFieldErrors(errors);requestAnimationFrame(()=>form.current?.querySelector('[aria-invalid="true"]')?.focus());}
  function advance(){const errors=validatePlanStep(draft,step);if(Object.keys(errors).length){showErrors(errors);return;}const next=step+1;setFurthest(current=>Math.max(current,next));move(next);}
  async function exportReport(withCost){
    if(lock.current||!plan||(withCost&&!cost))return;
    lock.current=true;setBusy(true);setError('');setDownloadNote('');
    try{const report=await previewRequest('report',{schemaVersion:1,plan:plan.input,...(withCost?{cost:cost.input}:{})});downloadReport(report);setDownloadNote('계획 JSON 다운로드를 요청했습니다. 파일은 설치가 실행되지 않은 계획 기록입니다.');}
    catch(e){setError(e.message||'보고서를 내보내지 못했습니다. 다시 시도하세요.');}
    finally{lock.current=false;setBusy(false);}
  }
  async function submit(kind){
    if(lock.current)return;
    if(kind==='plan'){
      for(let index=0;index<4;index++){const errors=validatePlanStep(draft,index);if(Object.keys(errors).length){setStep(index);showErrors(errors);return;}}
    }else{const errors=validatePrice(price);if(Object.keys(errors).length){showErrors(errors);return;}}
    lock.current=true;setBusy(true);setError('');setDownloadNote('');
    try{const out=await previewRequest(kind,kind==='plan'?draft:{...price,regionId:draft.regionId});if(kind==='plan'){setPlan(out);setFurthest(4);move(4);}else setCost(out);}
    catch(e){setError(e.message);}finally{lock.current=false;setBusy(false);}
  }
  function numberValue(event){return event.target.value===''?'':Number(event.target.value);}
  return <div className="preview-shell">
    <header className="preview-header"><div><span className="wordmark">Waiting Room<span className="preview-brand-dot">.</span></span><span className="preview-header-divider"/><span className="preview-header-label">설치 계획</span></div><span className="preview-badge">로컬 미리보기</span></header>
    <main className="preview-main">
      <aside className="preview-sidebar"><p className="preview-kicker">GET STARTED</p><h1>우리 서비스에 맞는<br/>대기열 준비하기</h1><p className="preview-subtitle">규모부터 운영 준비까지,<br/>한 단계씩 확인하세요.</p>
        <nav aria-label="설치 계획 단계"><ol className="preview-steps">{planSteps.map((label,index)=><li key={label} aria-current={step===index?'step':undefined}><button type="button" aria-label={`${label} 단계로 이동`} disabled={busy||index>furthest||(index>=4&&!plan)} onClick={()=>move(index)}><span className="preview-step-number">{index<step?'✓':index+1}</span><span>{label}{index===5?<small>선택 사항</small>:null}</span>{step===index?<span className="preview-step-arrow" aria-hidden="true">↗</span>:null}</button></li>)}</ol></nav>
        <div className="preview-selection"><small>현재 선택</small><strong>{high?'High Scale 100K':'Standard 10K'}</strong><span>{high?'Kubernetes · 외부 저장소':'단일 서버 · Docker Compose'}</span><span>{draft.regionId||'리전 미입력'}</span></div>
        <p className="preview-storage-note">입력은 단계 이동 시 유지됩니다.<br/>새로고침하면 초기화되며, 검토 후 JSON으로 보관할 수 있습니다.</p>
      </aside>
      <section className="preview-panel" aria-busy={busy}>
        <div className="preview-panel-topline"><span>STEP {String(step+1).padStart(2,'0')} <span className="preview-of">/ 06</span></span><span>{step===5?'선택 사항':'설치 전 계획'}</span></div>
        <h2 ref={heading} tabIndex={-1}>{planSteps[step]}</h2><p className="preview-description">{stepDescriptions[step]}</p>
        {step<4?<form ref={form} noValidate onSubmit={event=>{event.preventDefault();step===3?void submit('plan'):advance();}}><fieldset disabled={busy}>
          {step===0?<>
            <label className="preview-field"><span>설치 프로필</span><select name="profile" value={draft.profile} aria-invalid={fieldErrors.profile?true:undefined} onChange={e=>edit({...draft,profile:e.target.value})}><option value="standard-10k">Standard 10K · 단일 서버 / Compose</option><option value="high-scale-100k">High Scale 100K · Kubernetes / 외부 HA 저장소</option></select></label>
            {fieldErrors.profile?<p className="preview-field-error">{fieldErrors.profile}</p>:null}
            <div className="preview-profile-grid">{[false,true].map(isHigh=><button type="button" key={String(isHigh)} className={`preview-profile-card ${high===isHigh?'is-selected':''}`} aria-pressed={high===isHigh} onClick={()=>edit({...draft,profile:isHigh?'high-scale-100k':'standard-10k'})}><span className="preview-profile-top"><span>{isHigh?'확장 구성':'단일 서버 구성'}</span><span aria-hidden="true">{high===isHigh?'●':'○'}</span></span><strong>{isHigh?'High Scale':'Standard'}</strong><span className="preview-profile-cap">{isHigh?'100,000':'10,000'}<small> 방문 상태</small></span><span className="preview-profile-rule"/><span>{isHigh?'Kubernetes · 워커 3대':'Docker Compose · 서버 1대'}</span><span>{isHigh?'대당 8 vCPU / 16 GiB':'4 vCPU / 8 GiB / SSD 50 GiB'}</span><span>{isHigh?'외부 PostgreSQL·Valkey 직접 준비':'PostgreSQL·Valkey 포함 구성'}</span><span className="preview-profile-bottom">{isHigh?'클러스터 운영 환경이 있을 때':'단일 호스트로 시작할 때'}</span></button>)}</div>
            <div className="preview-info"><strong>10K와 100K는 무엇이 다른가요?</strong><p>대기 중·입장 가능·입장권 유효 상태를 합산한 상한입니다. 실제 동시 접속 성능을 보장하는 수치가 아니며, Standard는 단일 서버 장애에 대비한 HA를 제공하지 않습니다.</p></div>
          </>:null}
          {step===1?<>
            <Field label="Home Region" name="regionId" value={draft.regionId} maxLength={63} pattern="[a-z][a-z0-9\-]*" error={fieldErrors.regionId} hint="예: ap-northeast-2 · 대기열을 처리할 단일 리전 식별자" onChange={e=>edit({...draft,regionId:e.target.value})}/>
            <div className="preview-section-heading"><h3>{high?'클러스터 설치 전 준비물':'서버 설치 전 준비물'}</h3><span className="preview-pill">운영자 준비</span></div>
            <ol className="preview-requirements">{environmentItems[high?'high':'standard'].map(([title,description],index)=><li key={title}><span className="preview-requirement-index">{index+1}</span><div><strong>{title}</strong><p>{description}</p></div></li>)}</ol>
            <p className="preview-note">지금은 준비 항목을 안내하는 단계입니다. 서버·리전 존재 여부나 연결 상태를 검사하지 않습니다.</p>
          </>:null}
          {step===2?<>
            <div className="preview-capacity-banner"><div><small>{high?'High Scale':'Standard'} 방문 상태 한도</small><strong>{format(cap)}<small> 상태</small></strong></div><div><small>분당 신규 입장 상한</small><strong>{format(rate)}<small> /분</small></strong></div></div>
            <Field label="예상 방문 상태 수" name="expectedPeakVisitors" type="number" min={1} max={cap} value={draft.expectedPeakVisitors} error={fieldErrors.expectedPeakVisitors} hint="대기 중 + 입장 가능 + 아직 유효한 입장권 상태의 예상 최대 합계" onChange={e=>edit({...draft,expectedPeakVisitors:numberValue(e)})}/>
            <div className="preview-grid"><Field label="최대 활성 입장권 수" name="maxActiveAdmissionLeases" type="number" min={1} max={cap} value={draft.limits.maxActiveAdmissionLeases} error={fieldErrors.maxActiveAdmissionLeases} hint="동시에 유효한 입장권의 상한 · 현재 접속자 수와 다릅니다" onChange={e=>edit({...draft,limits:{...draft.limits,maxActiveAdmissionLeases:numberValue(e)}})}/>
              <Field label="분당 신규 입장 수" name="admissionsPerMinute" type="number" min={1} max={rate} value={draft.limits.admissionsPerMinute} error={fieldErrors.admissionsPerMinute} hint="원본 서비스가 처리할 수 있는 속도에 맞춰 설정하세요" onChange={e=>edit({...draft,limits:{...draft.limits,admissionsPerMinute:numberValue(e)}})}/></div>
            <Field label="입장권 유효 시간 (초)" name="admissionTtlSeconds" type="number" min={60} max={3600} value={draft.limits.admissionTtlSeconds} error={fieldErrors.admissionTtlSeconds} hint="60~3,600초 · 기본 900초(15분), 실제 체류 시간을 측정하는 값이 아닙니다" onChange={e=>edit({...draft,limits:{...draft.limits,admissionTtlSeconds:numberValue(e)}})}/>
            <div className="preview-info"><strong>먼저 온 순서대로, FIFO</strong><p>분당 {format(draft.limits.admissionsPerMinute||0)}개의 새 입장권을 허용하되 활성 입장권 한도에도 영향을 받습니다. 실제 입장 속도와 처리량은 연결 후 테스트로 확인해야 합니다.</p></div>
          </>:null}
          {step===3?<>
            <div className="preview-info"><strong>비밀번호에 인증 앱 코드를 더하세요</strong><p>TOTP를 사용하면 관리자 로그인에 일회용 인증 코드가 추가됩니다. 최초 설치 시 인증 앱을 연결하고 복구 코드를 따로 보관하게 됩니다.</p></div>
            <label className="preview-field"><span>TOTP 정책</span><select name="totpMode" value={draft.totp.mode} aria-invalid={fieldErrors.totpMode?true:undefined} onChange={e=>edit({...draft,totp:{mode:e.target.value,enabled:e.target.value==='forced_on'?true:draft.totp.enabled}})}><option value="configurable">운영자가 ON/OFF 선택</option><option value="forced_on">배포 정책으로 ON 강제</option></select></label>
            <label className="preview-check"><input name="totpEnabled" type="checkbox" checked={draft.totp.enabled} disabled={draft.totp.mode==='forced_on'} onChange={e=>edit({...draft,totp:{...draft.totp,enabled:e.target.checked}})}/><span><strong>TOTP 사용</strong><small>{draft.totp.mode==='forced_on'?'강제 ON 정책에서는 끌 수 없습니다.':'관리자 로그인에 인증 앱의 일회용 코드를 요구합니다.'}</small></span></label>
            {!draft.totp.enabled?<p className="preview-notice">TOTP를 끄면 비밀번호만으로 로그인하는 계획이 됩니다. 운영 환경에서는 인증 앱 사용을 권장합니다.</p>:null}
            <div className="preview-security-next"><h3>실제 설치에서 이어질 순서</h3><ol><li>첫 관리자 계정 만들기</li><li>인증 앱 연결 및 코드 확인</li><li>복구 코드 보관 후 콘솔 접속</li></ol><p className="preview-note">지금 선택은 계획에만 반영됩니다. 관리자 비밀번호나 인증 키를 입력할 필요가 없습니다.</p></div>
          </>:null}
          {Object.keys(fieldErrors).length?<p className="preview-field-error" role="alert">표시된 입력값을 수정한 뒤 다시 진행하세요.</p>:null}
          <div className="preview-actions"><span className="preview-action-note">{step===3?'서버에서 입력값을 검증합니다':'선택한 값은 다음 단계에도 유지됩니다'}</span><div>{step>0?<button type="button" className="preview-secondary" onClick={()=>move(step-1)}>이전 단계</button>:null}<button className="primary" type="submit">{busy?'검증 중…':step===3?'계획 생성':'다음 단계'}</button></div></div>
        </fieldset></form>:null}
        {step===4&&plan?<>
          <div className="preview-result" role="status"><span className="preview-result-icon" aria-hidden="true">✓</span><div><strong>입력 검증 완료 · 설치 미실행</strong><p>계획을 보관하거나 단가를 입력해 비용을 비교할 수 있습니다.</p></div></div>
          <div className="preview-review-grid"><ReviewSection title="규모" editLabel="규모 수정" busy={busy} onEdit={()=>move(0)}><dl><dt>프로필</dt><dd>{high?'High Scale 100K':'Standard 10K'}</dd><dt>방문 상태 한도</dt><dd>{format(plan.visitorStateCap)}</dd><dt>배포 구성</dt><dd>{high?'Kubernetes · 앱 리소스':'Docker Compose · 단일 서버'}</dd></dl></ReviewSection>
            <ReviewSection title="환경" editLabel="환경 수정" busy={busy} onEdit={()=>move(1)}><dl><dt>Home Region</dt><dd>{plan.input.regionId}</dd><dt>저장소</dt><dd>{high?'운영자가 외부 HA 저장소 준비':'단일 호스트에 포함'}</dd><dt>환경 검사</dt><dd>미실행</dd></dl></ReviewSection>
            <ReviewSection title="유량" editLabel="유량 수정" busy={busy} onEdit={()=>move(2)}><dl><dt>예상 방문 상태</dt><dd>{format(plan.input.expectedPeakVisitors)} 상태</dd><dt>최대 활성 입장권</dt><dd>{format(plan.input.limits.maxActiveAdmissionLeases)}개</dd><dt>신규 입장</dt><dd>{format(plan.input.limits.admissionsPerMinute)}개/분 · FIFO</dd><dt>입장권 유효 시간</dt><dd>{format(plan.input.limits.admissionTtlSeconds)}초</dd></dl></ReviewSection>
            <ReviewSection title="관리자 보안" editLabel="보안 수정" busy={busy} onEdit={()=>move(3)}><dl><dt>TOTP</dt><dd>{plan.input.totp.enabled?'ON':'OFF'} / {plan.input.totp.mode}</dd><dt>관리자 등록</dt><dd>설치 시 진행</dd></dl></ReviewSection></div>
          <details className="preview-resources"><summary>기준 자원 구성 · {plan.referenceResources.length}개 구성 요소</summary><div className="preview-table-scroll"><table><caption>Reference BOM · 실제 할당 또는 성능 검증 결과가 아닙니다</caption><thead><tr><th>구성 요소</th><th>수량</th><th>대당 CPU / 메모리</th><th>대당 저장 공간</th><th>준비 책임</th></tr></thead><tbody>{plan.referenceResources.map(resource=><tr key={resource.name}><th scope="row">{resource.name}</th><td>{resource.replicas}</td><td>{resource.cpuMillicoresPerReplica}m / {format(resource.memoryMiBPerReplica)} MiB</td><td>{resource.storageGiBPerReplica?`${resource.storageGiBPerReplica} GiB`:'별도'}</td><td>{resource.ownership==='operator-external'?'운영자 외부 준비':resource.ownership==='bundled-store'?'포함 저장소':'앱 구성'}</td></tr>)}</tbody></table></div><p className="preview-note">CPU 값은 계획 기준이며 전용 코어 보장이 아닙니다. OS·runtime 여유 자원과 별도 백업이 필요합니다.</p></details>
          <details className="preview-checks"><summary>설치·활성화 전에 남은 검사 <span>{plan.requiredChecksNotRun.length}개 미실행</span></summary><ul>{plan.requiredChecksNotRun.map(check=><li key={check}><span>{checkLabels[check]||check}</span><small>미실행</small></li>)}</ul></details>
          <div className="preview-next"><h3>로컬에서 첫 대기열을 실행해 보세요</h3><p>릴리스에서 CLI를 받은 뒤 Docker가 실행 중인 환경에서 아래 두 명령으로 설치와 첫 관리자 등록을 시작할 수 있습니다.</p><pre>./wrctl install{'\n'}./wrctl setup</pre><p>CLI는 prebuilt 이미지를 받아 로컬 데모 구성을 설치합니다. 이 10K/100K 계획 파일을 적용하거나 운영 환경·원본 보호를 검증하는 단계는 별도로 남아 있습니다.</p><a href="https://github.com/NudgeOn/Waiting-Room/blob/main/docs/operators/prebuilt-install.md" target="_blank" rel="noreferrer">설치와 업그레이드 안내 →</a></div>
          <Raw value={plan}/>
          <div className="preview-actions"><button className="preview-secondary" disabled={busy} onClick={()=>exportReport(false)}>계획 JSON 다운로드</button><button className="primary" disabled={busy} onClick={()=>{setFurthest(5);move(5);}}>비용 비교로 이동</button></div><button className="preview-link" disabled={busy} onClick={()=>move(0)}>설정 수정</button>
        </>:null}
        {step===5&&plan?<>
          <div className="preview-info"><strong>서버 + 디스크 소계만 비교합니다</strong><p>외부 HA 저장소·CDN/LB·egress·백업·세금은 제외됩니다. 실제 가격을 조회하지 않으므로 사용할 제공자의 단가를 직접 입력하세요.</p><span>가격 적용 리전: <strong>{draft.regionId}</strong></span></div>
          <form ref={form} noValidate onSubmit={e=>{e.preventDefault();void submit('estimate');}}><fieldset disabled={busy}><div className="preview-grid">{priceFields.map(([key,label,type,min,max,hint])=><Field key={key} name={key} label={label} hint={hint} error={fieldErrors[key]} type={type} min={min??undefined} max={max??undefined} inputMode={type==='text'&&key.endsWith('Hourly')||key==='volumeGiBMonthly'?'decimal':undefined} value={price[key]} maxLength={key==='currency'?3:63} onChange={e=>editPrice(key,type==='number'?numberValue(e):e.target.value)}/>)}</div>{Object.keys(fieldErrors).length?<p className="preview-field-error" role="alert">표시된 단가와 산정 조건을 확인하세요.</p>:null}<button type="submit" className="primary preview-calculate">{busy?'계산 중…':'소계 계산'}</button></fieldset></form>
          {cost?<section className="preview-cost-result" aria-label="비용 계산 결과" role="status"><h3>사용자 단가 기준 소계</h3><p>{cost.input.provider} · {cost.input.regionId} · {cost.input.asOf} · {cost.input.monthlyHours}시간</p><div className="preview-grid">{cost.profiles.map(profile=><div className="preview-cost-card" key={profile.profile}><h4>{profile.profile==='standard-10k'?'Standard 10K':'High Scale 100K'}</h4><strong className="preview-amount">{profile.roundedSubtotal} {cost.input.currency}</strong><small>/ 월 · 서버와 디스크</small><p>{profile.hosts}대 × {profile.vCPUPerHost} vCPU / {profile.memoryGiBPerHost} GiB<br/>대당 디스크 {profile.volumeGiBPerHost} GiB</p><dl>{profile.included.map(line=><React.Fragment key={line.id}><dt>{line.id==='compute'?'서버':'디스크'} · {format(line.quantity)} {line.unit} × {line.unitPrice}</dt><dd>{line.exactAmount} {cost.input.currency}</dd></React.Fragment>)}</dl></div>)}</div><p>포함 소계 배율: {cost.highToStandardRatio===null?'계산 불가 (Standard 소계 0)':cost.highToStandardRatio+'배'}</p><p className="preview-note">반올림 전 금액을 합산한 뒤 소수 {cost.input.fractionDigits}자리에서 half-up 반올림합니다. 외부 HA 저장소 등 제외 비용이 있어 총 운영비와 다릅니다.</p><Raw value={cost}/></section>:null}
          <div className="preview-actions"><button className="preview-secondary" disabled={busy} onClick={()=>move(4)}>계획으로 돌아가기</button><button className="primary" disabled={busy} onClick={()=>exportReport(Boolean(cost))}>{cost?'계획·비용 JSON 다운로드':'계획 JSON 다운로드'}</button></div>
        </>:null}
        {downloadNote?<p className="preview-note" role="status">{downloadNote}</p>:null}{error?<p className="error" role="alert">{error}</p>:null}
      </section>
    </main><footer className="preview-footer"><span>NudgeOn Waiting Room · Apache-2.0</span><span>계획 전용 / 서버 저장 없음 / 실제 설치 없음</span></footer>
  </div>;
}
