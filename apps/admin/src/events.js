// SPDX-License-Identifier: Apache-2.0
export const eventLabels={scheduled:'실행 예정',running:'실행 중',paused_by_override:'수동 변경으로 일시정지',cancelled:'취소됨',completed:'완료됨'};
export function localEventTime(value){
 const d=new Date(value);if(!Number.isFinite(d.getTime()))return '';
 const two=n=>String(n).padStart(2,'0');
 return `${d.getFullYear()}-${two(d.getMonth()+1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}
export function eventFromForm(form,now=Date.now()){
 const values=['prequeueAt','admitAt','drainAt'].map(key=>{
  const raw=String(form.get(key)??'');
  if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(raw))throw Error('예약 날짜와 시간을 모두 입력하세요.');
  const time=new Date(raw);
  if(!Number.isFinite(time.getTime())||localEventTime(time)!==(raw.length===16?raw+':00':raw))throw Error('선택한 시간대에 존재하지 않는 날짜나 시간입니다.');
  return [key,time.toISOString()];
 });
 const times=values.map(([,v])=>Date.parse(v));
 if(times[0]<=now||times[0]>=times[1]||times[1]>=times[2]||times[2]>now+366*86400000)throw Error('현재 이후 366일 이내에 사전 대기 → 입장 → 안전 종료 순서로 입력하세요.');
 return Object.fromEntries(values);
}
