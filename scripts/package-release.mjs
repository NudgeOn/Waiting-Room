// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFileSync} from 'node:child_process';

const version=process.env.VERSION,digest=process.env.IMAGE_DIGEST;
if(!/^v\d+\.\d+\.\d+-preview\.[1-9]\d*$/.test(version??'')||!/^sha256:[a-f0-9]{64}$/.test(digest??''))throw Error('Explicit Preview version and published image digest required');
const root=process.cwd(),output=path.join(root,'build/release'),image='ghcr.io/nudgeon/waiting-room@'+digest;
fs.mkdirSync(output,{recursive:true});
const sums=[];
for(const platform of ['linux','darwin'])for(const arch of ['amd64','arm64']){
  const name=`wrctl_${version}_${platform}_${arch}`,stage=path.join(root,'build/release-stage',name);
  fs.mkdirSync(stage,{recursive:true});
  execFileSync('go',['build','-trimpath','-ldflags',`-s -w -X waiting-room/internal/installer.DefaultImage=${image} -X waiting-room/internal/installer.Version=${version}`,'-o',path.join(stage,'wrctl'),'./cmd/wrctl'],{stdio:'inherit',env:{...process.env,CGO_ENABLED:'0',GOOS:platform,GOARCH:arch}});
  fs.copyFileSync('LICENSE',path.join(stage,'LICENSE'));
  fs.writeFileSync(path.join(stage,'runtime-image.txt'),image+'\n');
  fs.copyFileSync('docs/releases/quick-start.md',path.join(stage,'README.md'));
  const filename=name+'.tar.gz';
  execFileSync('tar',['-czf',path.join(output,filename),'-C',stage,'wrctl','LICENSE','runtime-image.txt','README.md'],{stdio:'inherit'});
  sums.push(crypto.createHash('sha256').update(fs.readFileSync(path.join(output,filename))).digest('hex')+'  '+filename);
}
fs.writeFileSync(path.join(output,'runtime-image.txt'),image+'\n');
fs.writeFileSync(path.join(output,'SHA256SUMS'),sums.join('\n')+'\n');
console.log(`Packaged ${sums.length} CLI archives for ${version}; each uses the same published runtime digest.`);
