// SPDX-License-Identifier: Apache-2.0
export const LOGO_MAX_BYTES=16384;
export const LOGO_HELP='PNG·JPEG, 16 KiB 이하, 가로·세로 최대 256px. 저장 시 이미지 정보와 부가 데이터를 제거하고 PNG로 변환합니다. 변환 결과도 16 KiB 이하여야 합니다.';

export function logoDimensions(bytes){
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  if(bytes.length>=24&&[137,80,78,71,13,10,26,10].every((v,i)=>bytes[i]===v))return {mime:'image/png',width:view.getUint32(16),height:view.getUint32(20)};
  if(bytes[0]===255&&bytes[1]===216&&bytes[2]===255){
    let offset=2;
    while(offset+4<=bytes.length){
      if(bytes[offset++]!==255)break;
      while(bytes[offset]===255)offset++;
      const marker=bytes[offset++];
      if(marker===218||marker===217||offset+2>bytes.length)break;
      const length=view.getUint16(offset);
      if(length<2||offset+length>bytes.length)break;
      if([192,193,194].includes(marker)&&length>=8)return {mime:'image/jpeg',height:view.getUint16(offset+3),width:view.getUint16(offset+5)};
      offset+=length;
    }
  }
  throw Error('PNG 또는 JPEG 이미지 파일을 선택하세요. SVG와 외부 주소는 사용할 수 없습니다.');
}
export async function readLogoFile(file){
  if(!file||file.size===0||file.size>LOGO_MAX_BYTES)throw Error('로고 파일은 16 KiB 이하여야 합니다.');
  const bytes=new Uint8Array(await file.arrayBuffer()),dimensions=logoDimensions(bytes);
  if(dimensions.width<1||dimensions.height<1||dimensions.width>256||dimensions.height>256||dimensions.width*dimensions.height>65536)throw Error('가로·세로가 각각 1~256px인 로고를 선택하세요.');
  return btoa(String.fromCharCode(...bytes));
}
export function logoPreviewURL(encoded){
  if(!encoded||encoded.length>21848)return undefined;
  try{const bytes=Uint8Array.from(atob(encoded),c=>c.charCodeAt(0)),d=logoDimensions(bytes);if(d.width<1||d.height<1||d.width>256||d.height>256)return undefined;return `data:${d.mime};base64,${encoded}`;}catch{return undefined;}
}
