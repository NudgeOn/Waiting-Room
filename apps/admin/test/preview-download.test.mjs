// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {downloadReport} from '../src/preview-download.js';
test('download uses fixed filename and JSON bytes, then cleans its temporary URL',async()=>{
  const old={document:globalThis.document,create:URL.createObjectURL,revoke:URL.revokeObjectURL,timer:globalThis.setTimeout};
  let blob,appended=false,clicked=false,removed=false,revoked=false,cleanup;
  const link={click(){clicked=true;},remove(){removed=true;}};
  globalThis.document={createElement:tag=>{assert.equal(tag,'a');return link;},body:{append:el=>{assert.equal(el,link);appended=true;}}};
  URL.createObjectURL=b=>{blob=b;return 'blob:local-fixture';};URL.revokeObjectURL=url=>{assert.equal(url,'blob:local-fixture');revoked=true;};globalThis.setTimeout=(fn,ms)=>{assert.equal(ms,1000);cleanup=fn;};
  try{const report={name:'<script>not HTML</script>',installation:'NOT_RUN'};downloadReport(report);assert.equal(blob.type,'application/json');assert.equal(await blob.text(),JSON.stringify(report,null,2)+'\n');assert.equal(link.download,'waiting-room-planning-report.json');assert.equal(link.href,'blob:local-fixture');assert.ok(appended&&clicked&&removed);cleanup();assert.ok(revoked);}
  finally{globalThis.document=old.document;URL.createObjectURL=old.create;URL.revokeObjectURL=old.revoke;globalThis.setTimeout=old.timer;}
});
