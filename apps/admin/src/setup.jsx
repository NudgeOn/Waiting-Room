// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {api,USER_PATTERN} from './api.js';
import {Field,Submit} from './forms.jsx';
import {downloadSetupReport,importSetupInput} from './setup.js';
import './setup.css';

const steps=['설치 확인','설정','환경 보정','검토 · 적용','관리자'];
export default function SetupWizard({busy,perform,accept}){
  const [step,setStep]=useState(0),[token,setToken]=useState(''),[inspection,setInspection]=useState(null),[input,setInput]=useState(null),[calibration,setCalibration]=useState(null),[review,setReview]=useState(null),[report,setReport]=useState(null),[approved,setApproved]=useState(false);
  const title=useRef(null);
  useEffect(()=>{title.current?.focus();},[step]);
  const request=(path,body={})=>api('/setup/'+path,{body,installToken:token});
  function edit(update){setInput(value=>({...value,...update}));setReview(null);setApproved(false);}
  function numeric(name,label,min,max,limits=false){const value=limits?input.limits[name]:input[name];return <Field label={label} name={name} type="number" min={min} max={max} step={1} value={value} onChange={e=>{const n=e.target.value===''?'':Number(e.target.value);edit(limits?{limits:{...input.limits,[name]:n}}:{[name]:n});}}/>;}
  async function inspect(event){event.preventDefault();await perform(async()=>{const out=await request('inspect');setInspection(out);setInput(out.input);setReport(out.report);setStep(out.report?4:1);});}
  async function calibrate(){await perform(async()=>{setCalibration(null);setReview(null);setApproved(false);const out=await request('calibrate');setCalibration(out);});}
  async function prepare(){await perform(async()=>{setReview(await request('plan',input));setApproved(false);setStep(3);});}
  async function apply(){await perform(async()=>{const out=await request('apply',{input:review.plan.input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});setReport(out);setInput(out.plan.input);setStep(4);});}
  return <div className="setup-wizard">
    <p className="setup-eyebrow">처음 한 번, 운영 준비</p><h2 ref={title} tabIndex={-1}>설치 위자드</h2>
    <ol className="setup-steps" aria-label="설치 단계">{steps.map((name,i)=><li key={name} aria-current={step===i?'step':undefined}><span>{i<step?'✓':i+1}</span>{name}</li>)}</ol>
    {step===0?<><h3>이 설치를 확인하세요</h3><p>터미널의 <code>wrctl setup</code>에 표시된 설치 토큰을 입력하세요. 이미 적용했다면 저장된 결과에서 이어집니다.</p><form onSubmit={inspect}><fieldset disabled={busy}><Field label="설치 토큰" name="installToken" type="password" autoComplete="off" minLength={43} maxLength={43} value={token} onChange={e=>setToken(e.target.value)}/><Submit busy={busy}>설치 확인</Submit></fieldset></form></>:null}
    {step===1?<><h3>설치 설정을 준비하세요</h3><p>Standard 10K · 로컬 Docker · 단일 리전. 입력한 유량은 새 Room의 시작값으로 저장합니다.</p>
      <label className="setup-import">저장한 계획 JSON 불러오기<input type="file" accept=".json,application/json" disabled={busy} onChange={e=>{const file=e.target.files?.[0];e.target.value='';if(file)perform(async()=>{const imported=await importSetupInput(file);if(inspection.input.totp.mode==='forced_on'&&imported.totp.mode!=='forced_on')throw Error('이 설치는 TOTP 필수 정책을 유지해야 합니다.');edit(imported);});}}/></label>
      <form onSubmit={e=>{e.preventDefault();setStep(2);}}><fieldset disabled={busy}>
        <Field label="리전 ID" name="regionId" pattern="[a-z](?:[a-z0-9]|-){0,62}" maxLength={63} value={input.regionId} onChange={e=>edit({regionId:e.target.value})}/>
        {numeric('expectedPeakVisitors','예상 최대 방문자 수',1,10000)}
        <div className="setup-fields">{numeric('maxActiveAdmissionLeases','최대 활성 입장권 수',1,10000,true)}{numeric('admissionsPerMinute','분당 신규 입장 수',1,6000,true)}</div>
        {numeric('admissionTtlSeconds','입장권 유효 시간 (초)',60,3600,true)}
        <label className="check"><input type="checkbox" checked={input.totp.enabled} disabled={input.totp.mode==='forced_on'} onChange={e=>edit({totp:{...input.totp,enabled:e.target.checked}})}/>관리자 TOTP 인증 사용</label>
        <label className="check"><input type="checkbox" checked={input.totp.mode==='forced_on'} disabled={inspection.input.totp.mode==='forced_on'} onChange={e=>edit({totp:{mode:e.target.checked?'forced_on':'configurable',enabled:e.target.checked||input.totp.enabled}})}/>TOTP를 필수로 고정</label>
        <p className="hint">TOTP를 필수로 고정하면 운영 중 끌 수 없습니다. 유량은 원본 서버의 처리 성능을 확인한 후 Room별로 조정하세요.</p><Submit busy={busy}>환경 확인으로</Submit>
      </fieldset></form></>:null}
    {step===2?<><h3>이 서버에 맞게 로그인 보정</h3><dl className="setup-summary"><dt>실행 환경</dt><dd>{inspection.environment.os} / {inspection.environment.arch}</dd><dt>Control 병렬 실행</dt><dd>{inspection.environment.goParallelism}</dd><dt>PostgreSQL</dt><dd>연결 확인 · 서버 간 시각 차이 {inspection.environment.databaseClockDifferenceMs} ms</dd><dt>관리자 주소</dt><dd>https://127.0.0.1:19443</dd><dt>Gateway 주소</dt><dd>https://127.0.0.1:20443</dd></dl>
      <p>Control 서버에서 비밀번호 검증 비용을 측정해 250~500ms 범위의 값을 선택합니다. 실제 비밀번호는 사용하지 않습니다.</p>
      <button className="primary" disabled={busy} onClick={calibrate}>{busy?'서버에서 측정 중…':calibration?'다시 측정':'로그인 성능 보정'}</button>
      {calibration?<div className="setup-result" role="status"><strong>{calibration.calibration.targetMet?'보정 완료':'목표 범위를 찾지 못했습니다'}</strong><p>{calibration.calibration.targetMet?`64 MiB · 반복 ${calibration.calibration.iterations}회 · 중앙값 ${calibration.calibration.samples.find(s=>s.iterations===calibration.calibration.iterations)?.medianMs}ms`:'서버의 CPU·메모리 할당과 부하를 확인하고 다시 측정하세요. 설정은 적용되지 않았습니다.'}</p></div>:null}
      <p className="hint">로컬 연결과 로그인 비용을 확인합니다. 호스트 NTP, 공개 DNS·TLS, 원본 우회 차단과 대규모 성능 검증은 별도입니다.</p>
      <div className="setup-actions"><button className="secondary" disabled={busy} onClick={()=>setStep(1)}>설정 수정</button><button className="primary" disabled={busy||!calibration?.calibration.targetMet} onClick={prepare}>적용 계획 검토</button></div>
    </>:null}
    {step===3&&review?<><h3>적용할 설정을 확인하세요</h3><dl className="setup-summary"><dt>프로필 · 리전</dt><dd>Standard 10K · {review.plan.input.regionId}</dd><dt>방문자 예상</dt><dd>{review.plan.input.expectedPeakVisitors}명</dd><dt>새 Room 시작값</dt><dd>활성 입장권 {review.plan.input.limits.maxActiveAdmissionLeases}개 · 분당 {review.plan.input.limits.admissionsPerMinute}명 · {review.plan.input.limits.admissionTtlSeconds}초</dd><dt>TOTP</dt><dd>{review.plan.input.totp.enabled?'ON':'OFF'}{review.plan.input.totp.mode==='forced_on'?' · 필수':''}</dd><dt>로그인 보정</dt><dd>64 MiB · 반복 {review.plan.calibration.iterations}회</dd></dl>
      <p>리전·프로필, 새 Room 시작값, 인증 정책과 보정 결과를 데이터베이스에 함께 저장합니다. Room 생성과 공개 트래픽 연결은 관리자 등록 후 진행합니다.</p>
      <label className="check"><input type="checkbox" checked={approved} disabled={busy} onChange={e=>setApproved(e.target.checked)}/>위 설정을 이 로컬 설치에 적용합니다.</label>
      <div className="setup-actions"><button className="secondary" disabled={busy} onClick={()=>{setStep(1);setReview(null);setApproved(false);}}>설정 수정</button><button className="primary" disabled={busy||!approved} onClick={apply}>{busy?'설정 저장 중…':'설정 적용'}</button></div>
    </>:null}
    {step===4&&report?<><div className="setup-result" role="status"><strong>설정 적용 완료</strong><p>리전 {report.plan.input.regionId} · 설정 버전 {report.configRevision}. 재시작 후에도 유지됩니다.</p></div><button className="text-button" onClick={()=>downloadSetupReport(report)}>설치 결과 JSON 다운로드</button><h3>첫 관리자 만들기</h3><p>TOTP {report.plan.input.totp.enabled?'ON · 다음 단계에서 인증 앱을 등록합니다.':'OFF'} · 15자 이상 비밀번호를 사용하세요.</p>
      <form onSubmit={e=>{e.preventDefault();const form=e.currentTarget,values=new FormData(form);perform(async()=>{const out=await api('/bootstrap',{body:{username:values.get('username'),password:values.get('password')},installToken:token});form.reset();setToken('');accept(out,values.get('username'));});}}><fieldset disabled={busy}><Field label="사용자 이름" name="username" autoComplete="username" pattern={USER_PATTERN} maxLength={128}/><Field label="비밀번호" name="password" type="password" autoComplete="new-password" minLength={15} maxLength={1024}/><Submit busy={busy}>관리자 만들기</Submit></fieldset></form>
    </>:null}
    {step>0?<button className="text-button" disabled={busy} onClick={()=>{setToken('');setReview(null);setCalibration(null);setApproved(false);setStep(0);}}>설치 토큰 다시 입력</button>:null}
  </div>;
}
