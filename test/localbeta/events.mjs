// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import path from 'node:path';
import {expect} from '@playwright/test';
import {adminSchema} from './contracts.mjs';
import {nativeDateInput} from './date-input.mjs';

export async function eventChecks({page,room,mark,screens}){
 await page.setViewportSize({width:360,height:900});
 const tab=name=>page.getByRole('navigation',{name:'Room 화면'}).getByRole('link',{name,exact:true}).click();
 await tab('일정');await expect(page).toHaveURL(new RegExp(`/rooms/${room.id}/schedule$`));
 const read=()=>page.evaluate(async id=>{
  const [v,e]=await Promise.all([fetch('/api/admin/v1/config/delivery'),fetch(`/api/admin/v1/rooms/${id}/events`)]);
  return {view:await v.json(),events:(await e.json()).items};
 },room.id);
 async function fill(offsets){
  const dates=offsets.map(s=>new Date(Date.now()+s*1000).toISOString());
  // Browser-local inputs: test runner and browser may use different timezones.
  const local=await page.evaluate(values=>values.map(v=>{const d=new Date(v),p=n=>String(n).padStart(2,'0');return `${d.getFullYear()}-${p(d.getMonth()+1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;}),dates);
  for(const [i,label] of ['사전 대기 시작','입장 시작','안전 종료 시작'].entries()){
   try{await page.getByLabel(label,{exact:true}).fill(nativeDateInput(local[i]));}catch(e){
    // This locator handles generated fixture dates only, never credentials.
    console.error('Schedule input '+label+' '+local[i]+': '+String(e.message).split('\n')[0].slice(0,240));throw e;
   }
  }
  return dates.map(v=>Math.floor(Date.parse(v)/1000)*1000);
 }
 async function submit(name,method,status=200){
  const [response]=await Promise.all([page.waitForResponse(r=>r.url().includes('/events')&&r.request().method()===method),page.getByRole('button',{name,exact:true}).click()]);
  assert.equal(response.status(),status);if(status===200)adminSchema('Event',await response.json());
  await expect(page.getByRole('button',{name:'예약 저장',exact:true})).toBeEnabled();
 }
 await page.getByRole('button',{name:'입력 초기화',exact:true}).click();
 await fill([300,360,420]);await submit('예약 저장','POST');
 let state=await read();const first=state.events[0];assert.equal(first.state,'scheduled');
 let row=page.locator(`[data-event-id="${first.id}"]`);await expect(row).toContainText('실행 예정');
 await row.getByRole('button',{name:'예약 수정',exact:true}).click();await fill([480,540,600]);await submit('예약 수정 저장','PUT');
 state=await read();assert.ok(Date.parse(state.events[0].prequeueAt)>Date.parse(first.prequeueAt));
 await fill([490,550,590]);await submit('예약 저장','POST',409);assert.equal((await read()).events.length,1);
 await row.getByRole('button',{name:'예약 취소',exact:true}).click();await expect(row).toContainText('취소됨');
 mark('Docker schedule UI creates and edits UTC events, overlapping schedule is rejected, cancelled event remains visible');

 await page.getByRole('button',{name:'입력 초기화',exact:true}).click();
 const times=await fill([12,24,50]);await submit('예약 저장','POST');
 state=await read();const event=state.events.find(e=>e.state==='scheduled');assert.ok(event);
 row=page.locator(`[data-event-id="${event.id}"]`);
 console.log('CHECK: real scheduler prequeue, pause past admission time, resume and drain');
 await expect.poll(async()=>{const s=await read();return [s.view.runtimes[0].runtime.mode,s.events.find(e=>e.id===event.id)?.state];},{timeout:22000}).toEqual(['HOLD','running']);
 await expect(row).toContainText('실행 중');
 await tab('운영');await page.getByRole('button',{name:'HOLD 일시정지',exact:true}).click();await expect(page.getByRole('status')).toContainText('명령을 저장했습니다.');await tab('일정');await expect(row).toContainText('수동 변경으로 일시정지');
 await expect.poll(()=>Date.now()>times[1]+1500,{timeout:20000}).toBe(true);
 state=await read();assert.equal(state.view.runtimes[0].runtime.mode,'HOLD');assert.equal(state.events.find(e=>e.id===event.id).state,'paused_by_override');
 await row.getByRole('button',{name:'예약 재개',exact:true}).click();
 await expect.poll(async()=>{const s=await read();return {mode:s.view.runtimes[0].runtime.mode,applied:s.view.state};},{timeout:15000}).toEqual({mode:'AUTO',applied:'applied'});
 await expect(row).toContainText('실행 중');mark('Real scheduler enters HOLD; manual override remains paused beyond admit time; explicit resume catches up to AUTO on both roles');
 await expect.poll(async()=>{const s=await read();return {mode:s.view.runtimes[0].runtime.mode,applied:s.view.state};},{timeout:40000}).toEqual({mode:'DRAINING',applied:'applied'});
 const drainRevision=(await read()).view.runtimes[0].runtime.revision;
 await expect(page.getByRole('button',{name:new RegExp(room.name)})).toContainText(`revision ${drainRevision} ·`);
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await page.screenshot({path:path.join(screens,'runtime-schedule-mobile.png'),fullPage:true});
 const [cancelResponse]=await Promise.all([page.waitForResponse(r=>r.url().endsWith(`/events/${event.id}`)&&r.request().method()==='DELETE'),row.getByRole('button',{name:'예약 취소',exact:true}).click()]);assert.equal(cancelResponse.status(),200);await expect(row).toContainText('취소됨');assert.equal((await read()).view.runtimes[0].runtime.mode,'DRAINING');
 await tab('운영');await page.getByRole('button',{name:'HOLD 일시정지',exact:true}).click();await expect.poll(async()=>(await read()).view.runtimes[0].runtime.mode).toBe('HOLD');
 mark('Real scheduler reaches DRAINING on both roles; cancellation does not silently bypass protection; 360px schedule UI fits');
}
