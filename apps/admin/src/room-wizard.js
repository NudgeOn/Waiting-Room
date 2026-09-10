// SPDX-License-Identifier: Apache-2.0
// Keep local guidance aligned with internal/control/config.go. The server remains
// authoritative; these checks never probe a hostname or an origin.
export const ROOM_STEPS = [
  {label:'연결',title:'어떤 서비스를 보호할까요?',description:'방문자가 접속하는 주소와 실제 서비스를 제공하는 원본 주소를 연결합니다.',fields:['id','name','hostname','origin','healthURL']},
  {label:'경로',title:'대기열을 적용할 경로를 정하세요',description:'결제·예약처럼 보호할 경로를 선택하고, 대기 없이 통과할 경로를 구분합니다.',fields:['protect','exclude']},
  {label:'유량',title:'서버가 감당할 만큼 입장시켜요',description:'먼저 도착한 순서대로 입장합니다. 서버의 처리량을 기준으로 세 값을 설정하세요.',fields:['leases','rate','ttl']},
  {label:'대기 화면',title:'기다리는 순간에도 서비스답게',description:'방문자에게 보여 줄 문구와 기본 색상을 정하세요. 오른쪽 미리보기에 바로 반영됩니다.',fields:['locale','color','title','message']},
  {label:'검토',title:'저장 전 검토',description:'설정을 확인하고 초안으로 저장하세요. 연결 검사와 운영 배포는 저장 후 별도로 진행합니다.',fields:['active']},
];

export function roomWizardValues(room){
  return {id:room.id,name:room.name,hostname:room.hostname,origin:room.origin,healthURL:room.healthURL,protect:room.protectPrefixes.join('\n'),exclude:room.excludePrefixes.join('\n'),leases:String(room.limits.maxActiveAdmissionLeases),rate:String(room.limits.admissionsPerMinute),ttl:String(room.limits.admissionTtlSeconds),locale:room.theme.locale,color:room.theme.primaryColor,title:room.theme.title,message:room.theme.message,logoImage:room.theme.logoImage||'',active:room.active};
}
export function prefixLines(value){return value.split('\n').map(line=>line.trim()).filter(Boolean);}
export function prefixMatches(prefix,path){const canonical=prefix.replace(/\/$/,'');return canonical===''||path===canonical||path.startsWith(canonical+'/');}
export function validRoomPath(path){return path.length>0&&path.length<=2048&&path.startsWith('/')&&!path.includes('//')&&!/[\x00-\x20\x7f-\uffff%\\?#;]/.test(path)&&!path.split('/').some(part=>part==='.'||part==='..');}
export function validRoomHostname(host){return host.length>0&&host.length<=253&&host.split('.').every(label=>/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));}

function httpsAddress(raw){
  const match=/^https:\/\/([^/?#]+)([^?#]*)$/.exec(raw);
  if(!match)return null;
  const [host,port,...extra]=match[1].split(':');
  if(!validRoomHostname(host)||extra.length||port!==undefined&&(!/^[1-9]\d{0,4}$/.test(port)||Number(port)>65535))return null;
  return {host,authority:match[1],path:match[2]};
}
function plain(value,max,multiline=false){return Array.from(value).length<=max&&!(multiline?/[<>\x00-\x08\x0b-\x1f\x7f-\x9f]/:/[<>\x00-\x1f\x7f-\x9f]/).test(value);}
export function roomProfileBounds(profile){return profile==='high-scale-100k'?{leases:100000,rate:60000}:{leases:10000,rate:6000};}

function pathListError(value,required){
  const paths=prefixLines(value);
  if(required&&!paths.length)return '보호할 경로를 하나 이상 입력하세요. 예: /shop';
  if(paths.length>32)return '경로는 최대 32개까지 입력할 수 있습니다.';
  const seen=new Set();
  for(const path of paths){
    if(!validRoomPath(path))return `${path}: /로 시작하는 경로만 입력하세요. 공백, 한글, %, ?, #, ;, \\, //, . 또는 .. 경로는 사용할 수 없습니다.`;
    if(prefixMatches('/_wr',path)||prefixMatches('/api/admin',path))return `${path}: 대기실과 관리자 전용 경로는 사용할 수 없습니다.`;
    const canonical=path.replace(/\/$/,'');
    if(seen.has(canonical))return `${path}: 같은 경로가 두 번 입력되었습니다. 끝의 /는 같은 경로로 처리됩니다.`;
    seen.add(canonical);
  }
  return '';
}

export function validateRoomWizard(values,{profile='standard-10k',rooms=[]}={}){
  const errors={};
  if(!/^[a-z][a-z0-9_-]{0,63}$/.test(values.id))errors.id='영문 소문자로 시작하고 소문자·숫자·-·_만 사용하세요. 최대 64자입니다.';
  else if(rooms.some(room=>room.id===values.id))errors.id='이미 사용 중인 Room ID입니다. 다른 ID를 입력하세요.';
  else if(rooms.length>=100)errors.id='한 설치에는 최대 100개의 Room을 저장할 수 있습니다. 기존 Room을 관리 화면에서 확인하세요.';
  if(!values.name.trim()||!plain(values.name,120))errors.name='표시 이름을 1~120자로 입력하세요. HTML 기호와 제어 문자는 사용할 수 없습니다.';
  if(!validRoomHostname(values.hostname))errors.hostname='https://와 경로 없이 소문자 호스트만 입력하세요. 예: shop.example.com';
  const origin=httpsAddress(values.origin),health=httpsAddress(values.healthURL);
  if(!origin||!['','/'].includes(origin.path))errors.origin='HTTPS 원본 주소를 입력하세요. 포트는 1~65535이며 경로·계정·쿼리는 넣지 않습니다. 예: https://origin.example.com';
  else if(origin.host===values.hostname)errors.origin='원본은 고객 호스트와 달라야 합니다. Gateway로 되돌아오지 않는 별도 원본 호스트를 입력하세요.';
  if(!health||!validRoomPath(health.path))errors.healthURL='HTTPS 주소에 상태 확인 경로를 포함하세요. 예: https://origin.example.com/health';
  else if(origin&&health.authority!==origin.authority)errors.healthURL='원본 HTTPS 주소와 같은 호스트·포트를 사용하세요. 상태 확인 경로만 추가할 수 있습니다.';
  for(const [field,required] of [['protect',true],['exclude',false]]){const error=pathListError(values[field],required);if(error)errors[field]=error;}
  const bounds=roomProfileBounds(profile);
  for(const [field,label,min,max] of [['leases','최대 활성 입장권 수',1,bounds.leases],['rate','분당 신규 입장 수',1,bounds.rate],['ttl','입장권 유효 시간',60,3600]]){
    if(!/^\d+$/.test(values[field])||!Number.isSafeInteger(Number(values[field]))||Number(values[field])<min||Number(values[field])>max)errors[field]=`${label}는 ${min.toLocaleString('ko-KR')}~${max.toLocaleString('ko-KR')} 사이의 정수로 입력하세요.`;
  }
  if(!['ko','en'].includes(values.locale))errors.locale='한국어 또는 English를 선택하세요.';
  if(!/^#[a-fA-F0-9]{6}$/.test(values.color))errors.color='색상을 #과 6자리 HEX로 입력하세요. 예: #105641';
  if(!values.title.trim()||!plain(values.title,120))errors.title='안내 제목을 1~120자로 입력하세요. HTML 기호는 사용할 수 없습니다.';
  if(!plain(values.message,1000,true))errors.message='안내 문구는 최대 1,000자이며 HTML 기호와 제어 문자는 사용할 수 없습니다.';
  if(values.active&&!errors.protect&&!errors.hostname){
    const collision=rooms.find(room=>room.active&&room.hostname===values.hostname&&room.protectPrefixes.some(other=>prefixLines(values.protect).some(path=>prefixMatches(path,other)||prefixMatches(other,path))));
    if(collision)errors.active=`${collision.name} (${collision.id})의 활성 보호 경로와 겹칩니다. 보호 경로를 수정하거나 활성화 선택을 해제하세요.`;
  }
  return errors;
}

export function roomFromWizard(values,room){return {...room,id:values.id,name:values.name,hostname:values.hostname,origin:values.origin,healthURL:values.healthURL,protectPrefixes:prefixLines(values.protect),excludePrefixes:prefixLines(values.exclude),limits:{maxActiveAdmissionLeases:Number(values.leases),admissionsPerMinute:Number(values.rate),admissionTtlSeconds:Number(values.ttl)},theme:{...room.theme,logoImage:values.logoImage||undefined,locale:values.locale,primaryColor:values.color,title:values.title,message:values.message},active:values.active};}

export function previewRoomRoute(path,values){
  if(!validRoomPath(path))return {kind:'invalid',label:'유효한 경로를 입력하세요',description:'도메인을 제외하고 /로 시작하는 경로를 입력하세요.'};
  if(prefixMatches('/_wr',path)||prefixMatches('/api/admin',path))return {kind:'reserved',label:'시스템 전용 경로',description:'방문자 보호 경로로 사용할 수 없습니다.'};
  if(prefixLines(values.exclude).some(prefix=>validRoomPath(prefix)&&prefixMatches(prefix,path)))return {kind:'excluded',label:'대기 없이 통과',description:'제외 경로가 보호 경로보다 우선합니다.'};
  if(prefixLines(values.protect).some(prefix=>validRoomPath(prefix)&&prefixMatches(prefix,path)))return {kind:'protected',label:'대기열 보호 대상',description:'이 Room이 배포되어 보호 모드일 때 대기열을 적용합니다.'};
  return {kind:'outside',label:'이 Room의 보호 범위 밖',description:'현재 입력한 보호 경로와 일치하지 않습니다.'};
}
