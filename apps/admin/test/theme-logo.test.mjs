// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {logoDimensions,logoPreviewURL,readLogoFile} from '../src/theme-logo.js';
import {newRoom} from '../src/control-api.js';
import {roomWizardValues,roomFromWizard} from '../src/room-wizard.js';

function pngHeader(width=32,height=16){const bytes=new Uint8Array(24);bytes.set([137,80,78,71,13,10,26,10]);const view=new DataView(bytes.buffer);view.setUint32(16,width);view.setUint32(20,height);return bytes;}
test('upload bounds are checked before reading or rendering pixels',async()=>{
  let read=false;await assert.rejects(readLogoFile({size:16385,arrayBuffer(){read=true;}}),/16 KiB/);assert.equal(read,false);
  for(const bytes of [new TextEncoder().encode('<svg onload="bad()"/>'),pngHeader(100000,1),pngHeader(1,0)])await assert.rejects(readLogoFile(new File([bytes],'logo.png',{type:'image/png'})));
  const good=await readLogoFile(new File([pngHeader()],'logo.png',{type:'application/octet-stream'}));assert.match(logoPreviewURL(good),/^data:image\/png;base64,/);
  for(const encoded of ['https://example.test/logo.png','data:image/svg+xml;base64,PHN2Zz4=',btoa('<svg/>'),btoa(String.fromCharCode(...pngHeader(100000,1)))])assert.equal(logoPreviewURL(encoded),undefined);
});
test('JPEG dimensions use bounded header parsing and reject truncated segments',()=>{
  const jpeg=Uint8Array.from([255,216,255,192,0,11,8,0,16,0,32,1,1,17,0]);assert.deepEqual(logoDimensions(jpeg),{mime:'image/jpeg',width:32,height:16});
  assert.throws(()=>logoDimensions(jpeg.slice(0,10)));
});
test('wizard and editor preserve a selected logo and explicitly remove it',()=>{
  const room=newRoom();room.theme.logoImage='aGVsbG8=';
  const values=roomWizardValues(room);assert.equal(values.logoImage,room.theme.logoImage);
  assert.equal(roomFromWizard(values,room).theme.logoImage,room.theme.logoImage);
  assert.equal(roomFromWizard({...values,logoImage:''},room).theme.logoImage,undefined);
});
