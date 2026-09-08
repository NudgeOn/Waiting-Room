// SPDX-License-Identifier: Apache-2.0
// Explicit local-only lifecycle. Never deletes volumes, credentials or accounts.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import net from 'node:net';
import {execFileSync,spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {waitForRuntime} from './runtime-readiness.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const project=process.env.WR_LOCAL_BETA_PROJECT??'waiting-room-local-beta';
if(project!=='waiting-room-local-beta'&&!/^waiting-room-local-beta-test-[a-f0-9]{8}$/.test(project))throw Error('Unsupported isolated local project');
const secretBase=path.join(root,'.cache',project==='waiting-room-local-beta'?'local-beta':project);
// Never accept a caller-supplied secret directory or compose file.
process.env.WR_LOCAL_BETA_SECRET_DIR=path.join(secretBase,'secrets');
const compose=['compose','-p',project,'-f','deploy/compose/local-beta.yaml'];
function run(bin,args,options={}){return execFileSync(bin,args,{cwd:root,stdio:'inherit',...options});}
function docker(args,options){return run('docker',[...compose,...args],options);}
function privateDirectory(dir){fs.mkdirSync(dir,{recursive:true,mode:0o700});const s=fs.lstatSync(dir);if(!s.isDirectory()||(s.mode&0o077))throw Error('Private non-symlink directory required');}
function prepareSecrets(){
  const base=secretBase,dir=path.join(base,'secrets');
  const names=['owner-password','runtime-password'];
  const files=names.map(n=>path.join(dir,n));
  const present=files.map(f=>fs.existsSync(f));
  if(present.some(Boolean)&&!present.every(Boolean))throw Error('Partial secret set; restore matching files. No credentials regenerated.');
  if(!present.some(Boolean)){
    // Refuse credential regeneration for a surviving DB or state volume.
    for(const name of [project+'_database',project+'_state']){
      const volumes=run('docker',['volume','ls','--format','{{.Name}}'],{encoding:'utf8',stdio:['ignore','pipe','pipe']}).trim().split('\n');
      if(volumes.includes(name))throw Error('Existing install volume with missing secrets; restore matching secret backup.');
    }
    privateDirectory(base);privateDirectory(dir);
    for(const file of files)fs.writeFileSync(file,crypto.randomBytes(32).toString('hex'),{mode:0o600,flag:'wx'});
  }
  for(const file of files){const s=fs.lstatSync(file);if(!s.isFile()||(s.mode&0o077)||s.size!==64||!/^[a-f0-9]{64}$/.test(fs.readFileSync(file,'utf8')))throw Error('Invalid private credential file');}
}

function setupTunnel(){
  const children=new Set(),sockets=new Set();
  const server=net.createServer(socket=>{
    if(sockets.size>=16){socket.destroy();return;}
    sockets.add(socket);socket.setTimeout(90000,()=>socket.destroy());
    const child=spawn('docker',[...compose,'exec','-T','control','/wr-control','tunnel'],{cwd:root,stdio:['pipe','pipe','ignore']});children.add(child);
    socket.pipe(child.stdin);child.stdout.pipe(socket);
    child.stdin.on('error',()=>socket.destroy());child.stdout.on('error',()=>socket.destroy());
    child.on('error',()=>socket.destroy());child.on('exit',()=>{children.delete(child);socket.destroy();});
    socket.on('error',()=>{});socket.on('close',()=>{sockets.delete(socket);child.kill('SIGTERM');});
  });
  server.on('error',()=>{console.error('Setup tunnel unavailable; check loopback port 19444.');process.exitCode=1;});
  server.listen(19444,'127.0.0.1',()=>console.log('Local setup tunnel: https://127.0.0.1:19444/setup (self-signed local TLS). Ctrl-C closes it. Port 19444 is never published by Docker.'));
  function stop(){server.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');}
  process.once('SIGINT',stop);process.once('SIGTERM',stop);
}

const [command,...args]=process.argv.slice(2);
try{
  if(command!=='init'&&args.length)throw Error('Unexpected arguments');
  switch(command){
    case 'build': {
      const arch=run('docker',['info','--format','{{.Architecture}}'],{encoding:'utf8',stdio:['ignore','pipe','pipe']}).trim();
      const goarch={aarch64:'arm64',arm64:'arm64',x86_64:'amd64',amd64:'amd64'}[arch];if(!goarch)throw Error('Unsupported Docker architecture');
      run('npm',['run','build:admin']);
      run('go',['build','-trimpath','-o','build/local-control/wr-control','./cmd/wr-control'],{env:{...process.env,CGO_ENABLED:'0',GOOS:'linux',GOARCH:goarch,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod')}});
      run('go',['build','-trimpath','-o','build/local-control/wr-node','./cmd/wr-node'],{env:{...process.env,CGO_ENABLED:'0',GOOS:'linux',GOARCH:goarch,GOCACHE:path.join(root,'.cache/go-build'),GOMODCACHE:path.join(root,'.cache/go-mod')}});
      const roots=['/etc/ssl/certs/ca-certificates.crt','/etc/ssl/cert.pem','/etc/pki/tls/certs/ca-bundle.crt'].find(p=>fs.existsSync(p));
      if(!roots)throw Error('System PEM CA bundle required; install system ca-certificates before building.');
      const bundle=fs.readFileSync(roots);if(!bundle.includes(Buffer.from('-----BEGIN CERTIFICATE-----'))||bundle.includes(Buffer.from('PRIVATE KEY')))throw Error('Invalid public CA bundle');
      fs.writeFileSync(path.join(root,'build/local-control/ca-certificates.crt'),bundle,{mode:0o644});
      console.log('System public CA bundle SHA-256: '+crypto.createHash('sha256').update(bundle).digest('hex'));
      docker(['build','control']);break;
    }
    case 'init':
      if(args.length!==1||!['on','off'].includes(args[0]))throw Error('Choose initial TOTP explicitly: init on | init off');
      prepareSecrets();docker(['up','-d','--wait','postgres']);docker(['run','--rm','initialize','init',args[0]]);docker(['up','-d','--wait','valkey']);docker(['run','--rm','queue-initialize']);break;
    case 'upgrade':
      // No implicit installation or secret regeneration in the upgrade path.
      for(const name of ['owner-password','runtime-password'])if(!fs.existsSync(path.join(secretBase,'secrets',name)))throw Error('Existing secret set required; upgrade never initializes a new installation.');
      prepareSecrets();docker(['stop','control','gateway','coordinator','demo-origin','valkey']);docker(['up','-d','--wait','postgres']);docker(['run','--rm','initialize','upgrade']);docker(['up','-d','--wait','valkey']);docker(['run','--rm','queue-initialize','queue-upgrade']);
      console.log('Control schema/known ACL upgraded; data preserved. Run up explicitly. Existing v3 queue data is not silently converted to v4.');break;
    case 'up':
      docker(['up','-d','control','coordinator','gateway','demo-origin']);
      console.log('Waiting for all six services; Coordinator recovery may require the longest configured ticket lifetime plus 30 seconds (up to about 62 minutes).');
      await waitForRuntime(()=>docker(['ps','--all','--format','json'],{encoding:'utf8',stdio:['ignore','pipe','pipe']}));break;
    case 'stop': docker(['stop']);break;
    case 'status': docker(['ps']);break;
    case 'bootstrap': docker(['run','--rm','bootstrap']);break;
    case 'token': docker(['exec','-T','control','/wr-control','token']);break;
    case 'setup': setupTunnel();break;
    default: throw Error('Usage: node scripts/local-beta.mjs build|init on|init off|upgrade|up|bootstrap|token|setup|status|stop');
  }
}catch(e){console.error(e.status===undefined?e.message:'Local Docker command failed. Existing data retained; inspect the failing stage.');process.exitCode=1;}
