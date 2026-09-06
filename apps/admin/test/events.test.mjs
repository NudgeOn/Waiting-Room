// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {eventFromForm,localEventTime} from '../src/events.js';
const form=values=>new Map(['prequeueAt','admitAt','drainAt'].map((key,i)=>[key,values[i]]));
test('local event inputs preserve seconds and serialize UTC',()=>{
 const now=Date.parse('2026-09-06T00:00:00Z'),dates=[60,120,180].map(s=>new Date(now+s*1000));
 const out=eventFromForm(form(dates.map(localEventTime)),now);
 assert.deepEqual(Object.values(out),dates.map(d=>d.toISOString()));
});
test('event form rejects missing, normalized calendar dates, past, reversed and oversized range',()=>{
 const now=new Date(2026,8,6).getTime(),valid=['2026-09-07T12:00','2026-09-07T13:00','2026-09-07T14:00'];
 for(const values of [['',...valid.slice(1)],['2026-02-30T12:00',...valid.slice(1)],['2026-09-05T12:00',...valid.slice(1)],[valid[1],valid[0],valid[2]],[valid[0],valid[0],valid[2]],[valid[0],valid[1],'2028-09-07T14:00'],['invalid',...valid.slice(1)]])assert.throws(()=>eventFromForm(form(values),now));
 assert.equal(localEventTime('invalid'),'');
 assert.match(eventFromForm(form(valid),now).prequeueAt,/Z$/);
});
