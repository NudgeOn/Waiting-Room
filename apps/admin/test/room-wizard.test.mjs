// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {newRoom} from '../src/control-api.js';
import {pageConnection,changeRoomConnection,previewRoomRoute,roomFromWizard,roomWizardValues,validateRoomWizard} from '../src/room-wizard.js';

const valid=()=>({...roomWizardValues(newRoom()),id:'sale',name:'가을 판매',hostname:'shop.example.test',origin:'https://origin.example.test',healthURL:'https://origin.example.test/health'});

test('Room wizard defaults stay inactive and preserve full API contract on save',()=>{
  const room=newRoom(),values=valid();assert.deepEqual(validateRoomWizard(values),{});
  const saved=roomFromWizard({...values,protect:'/shop\n\n /tickets ',exclude:'/shop/assets'},room);
  assert.equal(saved.active,false);assert.equal(saved.publicId,room.publicId);assert.deepEqual(saved.queuePolicy,room.queuePolicy);assert.deepEqual(saved.protectPrefixes,['/shop','/tickets']);assert.deepEqual(saved.excludePrefixes,['/shop/assets']);assert.deepEqual(saved.limits,room.limits);assert.equal(saved.theme.showEstimatedWait,true);
});
test('connection guidance rejects Gateway loops, credentials, normalization surprises and mismatched health URLs',()=>{
  for(const hostname of ['https://shop.test','SHOP.test','shop.test/cart','-shop.test','shop..test','한글.test'])assert.ok(validateRoomWizard({...valid(),hostname}).hostname,hostname);
  for(const origin of ['http://origin.test','https://user:password@origin.test','https://shop.example.test','https://origin.test/api','https://origin.test?','https://origin.test#','https://ORIGIN.test','https://origin.test:000443','https://origin.test:65536'])assert.ok(validateRoomWizard({...valid(),origin}).origin,origin);
  for(const healthURL of ['https://other.test/health','https://origin.example.test','https://origin.example.test/../health','https://origin.example.test/%68ealth','https://origin.example.test:443/health'])assert.ok(validateRoomWizard({...valid(),healthURL}).healthURL,healthURL);
  assert.deepEqual(validateRoomWizard({...valid(),origin:'https://origin.example.test:8443/',healthURL:'https://origin.example.test:8443/health'}),{});
});
test('prefix validation mirrors canonical and reserved path boundaries',()=>{
  for(const protect of ['', '/shop\n/shop/', '/shop//cart','/shop/../cart','/shop%2Fcart','/shop?x=1','/_wr/status','/api/admin/users','/shop;cart','/상품'])assert.ok(validateRoomWizard({...valid(),protect}).protect,protect);
  assert.ok(validateRoomWizard({...valid(),exclude:'/assets\n/assets/'}).exclude);
  assert.ok(validateRoomWizard({...valid(),protect:Array.from({length:33},(_,i)=>`/p${i}`).join('\n')}).protect);
  assert.deepEqual(validateRoomWizard({...valid(),protect:'/\n/shopping\n/_wr-other',exclude:'/assets'}),{});
});
test('route demonstration uses segment boundaries and exclusion precedence',()=>{
  const values={protect:'/shop',exclude:'/shop/assets'};
  assert.equal(previewRoomRoute('/shop/cart',values).kind,'protected');
  assert.equal(previewRoomRoute('/shopping',values).kind,'outside');
  assert.equal(previewRoomRoute('/shop/assets/logo.png',values).kind,'excluded');
  assert.equal(previewRoomRoute('/_wr/status',values).kind,'reserved');
  assert.equal(previewRoomRoute('/shop?x=1',values).kind,'invalid');
});
test('capacity allows only bounded integers in the installation profile',()=>{
  for(const value of ['0','1.5','1e3','-10','Infinity','10001',''])assert.ok(validateRoomWizard({...valid(),leases:value}).leases,value);
  assert.ok(validateRoomWizard({...valid(),rate:'6001'}).rate);
  assert.ok(validateRoomWizard({...valid(),ttl:'59'}).ttl);
  assert.ok(validateRoomWizard({...valid(),ttl:'3601'}).ttl);
  assert.deepEqual(validateRoomWizard({...valid(),leases:'100000',rate:'60000'},{profile:'high-scale-100k'}),{});
});
test('existing ID and active route conflicts are explained before saving',()=>{
  assert.ok(validateRoomWizard(valid(),{rooms:[{id:'sale'}]}).id);
  const rooms=[{id:'existing',name:'기존 판매',hostname:'shop.example.test',active:true,protectPrefixes:['/shop/cart']}];
  assert.equal(validateRoomWizard(valid(),{rooms}).active,undefined);
  assert.match(validateRoomWizard({...valid(),active:true},{rooms}).active,/기존 판매/);
  assert.equal(validateRoomWizard({...valid(),active:true,protect:'/shopping'},{rooms}).active,undefined);
});
test('theme checks reject unsafe text but retain multiline plain guidance',()=>{
  assert.ok(validateRoomWizard({...valid(),title:'<script>'}).title);
  assert.ok(validateRoomWizard({...valid(),message:'\rhidden'}).message);
  assert.ok(validateRoomWizard({...valid(),color:'red'}).color);
  assert.deepEqual(validateRoomWizard({...valid(),message:'차례가 오면\n안내합니다.\t감사합니다.',color:'#2357A5',locale:'en'}),{});
});

test('page-first connection derives the proxy route and preserves a custom waiting hostname',()=>{
  let v=changeRoomConnection(roomWizardValues(newRoom()),'targetURL','https://shop.example.com/sale');
  assert.equal(v.origin,'https://shop.example.com');assert.equal(v.hostname,'waiting.shop.example.com');assert.equal(v.protect,'/sale');assert.equal(v.healthURL,'https://shop.example.com/health');assert.equal(v.ttl,'60');
  v=changeRoomConnection(v,'hostname','https://queue.other.com');assert.equal(v.hostname,'queue.other.com');assert.equal(v.addressMode,'custom');
  v=changeRoomConnection(v,'targetURL','https://tickets.example.com/event');assert.equal(v.hostname,'queue.other.com');assert.equal(v.protect,'/event');
  v=changeRoomConnection(v,'addressMode','subdomain');assert.equal(v.hostname,'waiting.tickets.example.com');
  const saved=roomFromWizard({...v,id:'sale',name:'판매'},newRoom());assert.equal(saved.origin,'https://tickets.example.com');assert.deepEqual(saved.protectPrefixes,['/event']);assert.equal('targetURL' in saved,false);
});
test('page helper rejects unsafe URL boundaries without fetching the target',()=>{
  for(const raw of ['http://shop.test/sale','https://user:pw@shop.test/sale','https://shop.test/sale?key=value','https://shop.test/#x','https://shop.test/a/../b','https://shop.test/%2fsale','https://shop.test/_wr/status','https://shop.test/api/admin/login'])assert.equal(pageConnection(raw),null,raw);
  assert.equal(pageConnection('https://shop.test:8443').origin,'https://shop.test:8443');
});
