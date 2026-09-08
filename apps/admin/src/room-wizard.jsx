// SPDX-License-Identifier: Apache-2.0
import React,{useEffect,useRef,useState} from 'react';
import {ROOM_STEPS,prefixLines,previewRoomRoute,roomFromWizard,roomProfileBounds,roomWizardValues,validateRoomWizard} from './room-wizard.js';
import './room-wizard.css';

const number=value=>Number(value).toLocaleString('ko-KR');
const presets=[{name:'소규모 시작',detail:'입장권 100개 · 분당 60명',leases:'100',rate:'60',ttl:'900'},{name:'기본 설정',detail:'입장권 1,000개 · 분당 600명',leases:'1000',rate:'600',ttl:'900'}];

function Field({name,label,hint,error,children,...props}){
  const id=`room-${name}`,description=[hint?`${id}-hint`:null,error?`${id}-error`:null].filter(Boolean).join(' ')||undefined;
  return <div className="rw-field"><label htmlFor={id}>{label}</label>{children?React.cloneElement(children,{id,name,'aria-describedby':description,'aria-invalid':Boolean(error)}):<input id={id} name={name} aria-describedby={description} aria-invalid={Boolean(error)} {...props}/>}{hint?<p className="rw-hint" id={`${id}-hint`}>{hint}</p>:null}{error?<p className="rw-field-error" id={`${id}-error`}>{error}</p>:null}</div>;
}

function WaitingPreview({values,step}){
  const en=values.locale==='en',color=/^#[a-fA-F0-9]{6}$/.test(values.color)?values.color:'#105641';
  return <aside className="rw-preview-column" aria-label="대기 화면 미리보기">
    <div className="rw-preview-label"><strong>방문자 화면 미리보기</strong><span>CALM · {en?'EN':'KR'}</span></div>
    <div className="rw-browser"><div className="rw-browser-bar"><span className="rw-browser-dots" aria-hidden="true">● ● ●</span><span>{values.hostname||'고객 호스트'}</span><span className="rw-preview-tag">예시</span></div>
      <div className="rw-waiting" lang={en?'en':'ko'} style={{'--room-accent':color}}>
        <div className="rw-waiting-brand"><strong>Waiting Room</strong><span>{en?'English':'한국어'}</span></div>
        <div className="rw-waiting-intro"><span className="rw-waiting-mark" aria-hidden="true"><i/><i/><i/></span><h4>{values.title||(en?'Please wait a moment':'잠시만 기다려 주세요')}</h4><p>{values.message}</p></div>
        <div className="rw-waiting-status"><div><strong>{en?'Admission status':'입장 상태'}</strong><span><i aria-hidden="true"/>{en?'Waiting':'대기 중'}</span></div><p>{en?'Your place in line is kept when you refresh.':'새로고침해도 순서는 유지돼요.'}</p></div>
        <p className="rw-waiting-powered">Powered by Waiting Room</p>
      </div>
    </div>
    <p className="rw-preview-note">입력한 문구·색상·언어를 보여 주는 예시입니다. 실제 접속이나 대기열 상태를 조회하지 않습니다.</p>
    {step!==3?<div className="rw-overview"><strong>이번 Room의 설정</strong><dl><dt>보호 경로</dt><dd>{prefixLines(values.protect).join(', ')||'미입력'}</dd><dt>분당 신규 입장</dt><dd>{/^\d+$/.test(values.rate)?`${number(values.rate)}명`:'미입력'}</dd><dt>최대 활성 입장권</dt><dd>{/^\d+$/.test(values.leases)?`${number(values.leases)}개`:'미입력'}</dd></dl></div>:<div className="rw-overview"><strong>Calm · 기본 제공 템플릿</strong><p>간결한 상태 안내와 자동 순서 확인을 제공해요. 직접 입력한 안내 문구는 언어를 바꿔도 자동 번역되지 않습니다.</p></div>}
  </aside>;
}

function ReviewSection({index,title,onEdit,children}){return <section className="rw-review-section"><div><h4>{title}</h4><button className="rw-link" type="button" onClick={()=>onEdit(index)} aria-label={`${title} 수정`}>수정 <span aria-hidden="true">↗</span></button></div>{children}</section>;}

export default function RoomWizard({room,rooms,profile,busy,onSave,onCancel,onReload,saveError}){
  const [values,setValues]=useState(()=>roomWizardValues(room)),[step,setStep]=useState(0),[furthest,setFurthest]=useState(0),[touched,setTouched]=useState({}),[attempted,setAttempted]=useState(false),[testPath,setTestPath]=useState('/shop/cart');
  const heading=useRef(null),form=useRef(null);
  const errors=validateRoomWizard(values,{profile,rooms}),bounds=roomProfileBounds(profile),routeResult=previewRoomRoute(testPath,values),current=ROOM_STEPS[step];
  useEffect(()=>{heading.current?.focus();},[step]);
  function change(name,value){setValues(previous=>({...previous,[name]:value}));}
  function input(name){return {value:values[name],onChange:event=>change(name,event.target.value),onBlur:()=>setTouched(previous=>({...previous,[name]:true})),error:touched[name]?errors[name]:null};}
  function jump(index){setAttempted(false);setStep(index);}
  function focusError(name,index){setStep(index);requestAnimationFrame(()=>form.current?.elements.namedItem(name)?.focus());}
  function advance(){
    setTouched(previous=>({...previous,...Object.fromEntries(current.fields.map(name=>[name,true]))}));
    const invalid=current.fields.find(name=>errors[name]);
    if(invalid){setAttempted(true);focusError(invalid,step);return;}
    const next=Math.min(step+1,4);setFurthest(previous=>Math.max(previous,next));jump(next);
  }
  function submit(event){
    event.preventDefault();if(step<4){advance();return;}
    const invalid=Object.keys(errors)[0];
    if(invalid){setTouched(Object.fromEntries(Object.keys(values).map(name=>[name,true])));setAttempted(true);focusError(invalid,ROOM_STEPS.findIndex(item=>item.fields.includes(invalid)));return;}
    onSave(roomFromWizard(values,room));
  }
  function changeLocale(locale){
    setValues(previous=>({...previous,locale,...(['잠시만 기다려 주세요','Please wait a moment'].includes(previous.title)?{title:locale==='en'?'Please wait a moment':'잠시만 기다려 주세요'}:{}),...(['입장 가능한 순서가 되면 안내합니다.','We will let you know when it is your turn.'].includes(previous.message)?{message:locale==='en'?'We will let you know when it is your turn.':'입장 가능한 순서가 되면 안내합니다.'}:{})}));
  }
  const count=ROOM_STEPS.slice(0,4).filter((item,index)=>index<furthest&&!item.fields.some(name=>errors[name])).length;
  return <div className="room-wizard">
    <nav className="rw-progress" aria-label="Room 생성 단계"><ol>{ROOM_STEPS.map((item,index)=>{const completed=index<furthest&&!item.fields.some(name=>errors[name]);return <li key={item.label} aria-current={step===index?'step':undefined}><button type="button" disabled={busy||index>furthest} onClick={()=>jump(index)} aria-label={`${index+1}. ${item.label}${completed?' · 입력 완료':''}`}><span className="rw-step-number" aria-hidden="true">{completed?'✓':index+1}</span><span>{item.label}</span></button></li>;})}</ol><span className="rw-progress-count">{count}/4 입력 완료</span></nav>
    <div className="rw-layout"><form className="rw-form" noValidate ref={form} onSubmit={submit}>
      <div className="rw-step-heading"><span className="rw-eyebrow">STEP {String(step+1).padStart(2,'0')} / 05</span><h3 ref={heading} tabIndex={-1}>{current.title}</h3><p>{current.description}</p></div>
      {attempted&&current.fields.some(name=>errors[name])?<p className="rw-error-summary" role="alert">표시된 입력값을 수정한 뒤 다시 진행하세요.</p>:null}
      <fieldset disabled={busy} className="rw-fields"><legend className="rw-sr-only">{current.label} 설정</legend>
        {step===0?<>
          <div className="rw-two-columns"><Field name="name" label="표시 이름" hint="운영 화면에 표시할 이름입니다." placeholder="예: 가을 한정 판매" maxLength={120} required {...input('name')}/><Field name="id" label="Room ID" hint="고유한 식별자입니다. 저장 후 바꿀 수 없습니다." placeholder="예: autumn-sale" maxLength={64} autoCapitalize="none" spellCheck={false} required {...input('id')}/></div>
          <Field name="hostname" label="고객 호스트" hint="방문자가 접속하는 주소입니다. https://와 경로는 제외하세요." placeholder="shop.example.com" autoCapitalize="none" spellCheck={false} required {...input('hostname')}/>
          <Field name="origin" label="원본 HTTPS 주소" hint="Gateway를 경유하지 않는 실제 서버 주소입니다. 고객 호스트와 다른 호스트를 사용하세요." type="url" placeholder="https://origin.example.com" autoCapitalize="none" spellCheck={false} required {...input('origin')}/>
          <Field name="healthURL" label="원본 상태 확인 URL" hint="원본과 같은 호스트·포트에서 상태 확인에 응답하는 경로를 입력하세요." type="url" placeholder="https://origin.example.com/health" autoCapitalize="none" spellCheck={false} required {...input('healthURL')}/>
          <div className="rw-callout"><span aria-hidden="true">↳</span><p><strong>주소 입력만으로 연결되지는 않아요.</strong> 저장 후 원본 연결과 Gateway 경유를 확인하고 별도로 배포합니다.</p></div>
        </>:null}
        {step===1?<>
          <Field name="protect" label="보호 경로 (한 줄에 하나)" hint="최대 32개. /shop은 /shop/cart까지 포함하지만 /shopping은 포함하지 않습니다." error={touched.protect?errors.protect:null}><textarea value={values.protect} rows={3} onChange={event=>change('protect',event.target.value)} onBlur={()=>setTouched(previous=>({...previous,protect:true}))} spellCheck={false}/></Field>
          <Field name="exclude" label="제외 경로 (한 줄에 하나)" hint="선택 사항 · 상태 확인이나 정적 파일처럼 대기 없이 통과할 경로입니다. 제외가 항상 우선합니다." error={touched.exclude?errors.exclude:null}><textarea value={values.exclude} rows={3} placeholder={'/shop/assets\n/shop/health'} onChange={event=>change('exclude',event.target.value)} onBlur={()=>setTouched(previous=>({...previous,exclude:true}))} spellCheck={false}/></Field>
          {prefixLines(values.protect).some(path=>prefixLines(values.exclude).some(exclude=>previewRoomRoute(path,{protect:'',exclude}).kind==='excluded'))?<p className="rw-callout">제외 경로가 보호 경로 전체를 포함하고 있습니다. 의도한 범위인지 확인하세요.</p>:null}
          <div className="rw-path-check"><div className="rw-section-caption"><strong>경로 적용 미리보기</strong><span>입력값 기준</span></div><Field name="testPath" label="확인할 경로" hint="설정과 비교만 합니다. 실제 URL에 요청하지 않습니다." value={testPath} onChange={event=>setTestPath(event.target.value)} spellCheck={false}/><div className={`rw-route-result rw-route-${routeResult.kind}`} aria-live="polite"><strong>{routeResult.kind==='protected'?'✓ ':''}{routeResult.label}</strong><p>{routeResult.description}</p></div></div>
        </>:null}
        {step===2?<>
          <div className="rw-presets" role="group" aria-label="유량 시작값">{presets.map(preset=><button type="button" key={preset.name} aria-pressed={values.leases===preset.leases&&values.rate===preset.rate&&values.ttl===preset.ttl} onClick={()=>setValues(previous=>({...previous,leases:preset.leases,rate:preset.rate,ttl:preset.ttl}))}><strong>{preset.name}</strong><span>{preset.detail}</span></button>)}</div>
          <p className="rw-hint rw-preset-note">시작값 예시입니다. 실제 서버 용량을 검증한 권장값은 아닙니다.</p>
          <Field name="leases" label="최대 활성 입장권 수" hint={`동시에 유효한 입장권 한도입니다. 현재 접속자 수와 다릅니다. 최대 ${number(bounds.leases)}개.`} type="number" min={1} max={bounds.leases} step={1} inputMode="numeric" required {...input('leases')}/>
          <Field name="rate" label="분당 신규 입장 수" hint={`새로운 입장권을 발급하는 속도입니다. 최대 ${number(bounds.rate)}명/분.`} type="number" min={1} max={bounds.rate} step={1} inputMode="numeric" required {...input('rate')}/>
          <Field name="ttl" label="입장권 유효 시간 (초)" hint="60~3,600초. 입장 후 서비스 이용에 충분한 시간으로 정하세요. 기본 900초는 15분입니다." type="number" min={60} max={3600} step={1} inputMode="numeric" required {...input('ttl')}/>
          <div className="rw-callout"><span className="rw-fifo">FIFO</span><p><strong>먼저 온 순서대로 입장해요.</strong> 현재 설치 프로필은 {profile==='high-scale-100k'?'High Scale 100K':'Standard 10K'}입니다. 위 한도는 설정 범위이며 실제 처리 성능을 보장하지 않습니다.</p></div>
        </>:null}
        {step===3?<>
          <div className="rw-template"><span className="rw-template-art" aria-hidden="true"><i/><i/><i/></span><div><strong>Calm</strong><p>차분한 기본 대기 화면 · 기본 제공</p></div><span className="rw-selected-label">선택됨</span></div>
          <div className="rw-two-columns"><Field name="locale" label="언어" hint="상태 안내에 사용할 언어입니다." error={touched.locale?errors.locale:null}><select value={values.locale} onChange={event=>changeLocale(event.target.value)}><option value="ko">한국어</option><option value="en">English</option></select></Field><Field name="color" label="기본 색상 (HEX)" hint="상태 표시와 포인트 색상입니다." maxLength={7} spellCheck={false} {...input('color')}/></div>
          <div className="rw-swatches" role="group" aria-label="기본 색상 선택">{[['#105641','포레스트'],['#2357A5','블루'],['#6A458B','퍼플'],['#9A501E','앰버']].map(([color,label])=><button key={color} type="button" style={{'--swatch':color}} aria-label={`${label} ${color}`} aria-pressed={values.color.toLowerCase()===color.toLowerCase()} onClick={()=>change('color',color)}><span aria-hidden="true"/></button>)}</div>
          <Field name="title" label="안내 제목" hint={`${Array.from(values.title).length}/120자 · 방문자가 처음 보게 되는 안내입니다.`} maxLength={120} required {...input('title')}/>
          <Field name="message" label="안내 문구" hint={`${Array.from(values.message).length}/1,000자 · 예상하지 못한 대기 시간을 약속하기보다 입장 방식과 필요한 행동을 안내하세요.`} error={touched.message?errors.message:null}><textarea value={values.message} maxLength={1000} rows={4} onChange={event=>change('message',event.target.value)} onBlur={()=>setTouched(previous=>({...previous,message:true}))}/></Field>
        </>:null}
        {step===4?<>
          <div className="rw-review-state"><span aria-hidden="true">✓</span><div><strong>설정 입력을 마쳤어요</strong><p>항목별 수정 버튼으로 돌아가도 입력값은 유지됩니다.</p></div></div>
          <div className="rw-review-grid">
            <ReviewSection index={0} title="연결" onEdit={jump}><dl><dt>Room</dt><dd>{values.name} <small>{values.id}</small></dd><dt>고객 호스트</dt><dd>{values.hostname}</dd><dt>원본</dt><dd>{values.origin}</dd><dt>상태 확인</dt><dd>{values.healthURL}</dd></dl></ReviewSection>
            <ReviewSection index={1} title="경로" onEdit={jump}><dl><dt>보호</dt><dd>{prefixLines(values.protect).join(', ')}</dd><dt>제외</dt><dd>{prefixLines(values.exclude).join(', ')||'없음'}</dd></dl></ReviewSection>
            <ReviewSection index={2} title="유량" onEdit={jump}><dl><dt>입장 순서</dt><dd>FIFO · 먼저 온 순서</dd><dt>활성 입장권</dt><dd>최대 {number(values.leases)}개</dd><dt>신규 입장</dt><dd>{number(values.rate)}명 / 분</dd><dt>유효 시간</dt><dd>{number(values.ttl)}초</dd></dl></ReviewSection>
            <ReviewSection index={3} title="대기 화면" onEdit={jump}><dl><dt>템플릿 / 언어</dt><dd>Calm · {values.locale==='ko'?'한국어':'English'}</dd><dt>색상</dt><dd><i className="rw-review-color" style={{background:values.color}}/>{values.color}</dd><dt>제목</dt><dd>{values.title}</dd><dt>문구</dt><dd>{values.message||'없음'}</dd></dl></ReviewSection>
          </div>
          <div className="rw-after-save"><strong>저장 후 남은 단계</strong><ol><li><span>1</span>원본 연결과 보호 경로 확인</li><li><span>2</span>설정 배포 및 적용 상태 확인</li><li><span>3</span>입장 흐름 검증 후 운영 시작</li></ol><p>현재 화면은 초안만 저장합니다. 원본 연결 검사·서명 배포·대기열 활성화는 실행하지 않습니다. 샘플 환경의 Quick 20·Smoke 1K는 Traffic Lab에서 별도로 실행할 수 있습니다.</p></div>
          <label className="rw-activation"><input type="checkbox" name="active" checked={values.active} onChange={event=>{change('active',event.target.checked);setTouched(previous=>({...previous,active:true}));}} aria-describedby="room-active-hint" aria-invalid={Boolean(errors.active)}/><span><strong>배포 시 이 Room 보호 활성화</strong><small id="room-active-hint">선택하지 않으면 비활성 초안으로 저장합니다. 선택해도 지금 활성화되지 않으며 첫 배포 모드는 HOLD입니다.</small></span></label>
          {errors.active?<p className="rw-field-error" role="alert">{errors.active}</p>:null}
        </>:null}
      </fieldset>
      {saveError?<div className="rw-save-error" role="alert"><strong>초안을 저장하지 못했습니다.</strong><p>{saveError}</p><p>입력값은 이 화면에 유지됩니다.</p><button type="button" className="rw-link" disabled={busy} onClick={onReload}>입력을 유지하고 최신 초안 불러오기</button></div>:null}
      <div className="rw-footer"><div><button className="secondary" type="button" disabled={busy} onClick={step>0?()=>jump(step-1):onCancel}>{step>0?'이전 단계':'목록으로'}</button><span>{step===4?'저장해도 운영에 반영되지 않습니다.':'입력값은 단계 이동 시 유지됩니다.'}</span></div><button type="submit" className="primary" disabled={busy}>{busy?'저장 중…':step===4?'초안 저장':'다음 단계'}{!busy&&step<4?<span aria-hidden="true"> →</span>:null}</button></div>
    </form><WaitingPreview values={values} step={step}/></div>
  </div>;
}
