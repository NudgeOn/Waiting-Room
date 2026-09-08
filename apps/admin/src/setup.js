// SPDX-License-Identifier: Apache-2.0
import {validatePlanStep} from './preview-inputs.js';
export async function importSetupInput(file){
  if(file.size>16384)throw Error('16KB 이하 계획 JSON을 선택하세요.');
  let data;try{data=JSON.parse(await file.text());}catch{throw Error('계획 JSON을 읽을 수 없습니다.');}
  const input=data?.payload?.plan?.input??data?.plan?.input??data?.input??data;
  if(input?.schemaVersion!==1||input.profile!=='standard-10k'||input.queuePolicy!=='fifo'||!input.limits||!input.totp||typeof input.totp.enabled!=='boolean'||!['configurable','forced_on'].includes(input.totp.mode))throw Error('Standard 10K 설치 계획을 선택하세요.');
  if([0,1,2,3].some(step=>Object.keys(validatePlanStep(input,step)).length))throw Error('계획의 리전·유량·보안 입력값을 확인하세요.');
  // Import only the public planning contract; never arbitrary fields or credentials.
  return {schemaVersion:1,profile:input.profile,regionId:input.regionId,queuePolicy:'fifo',expectedPeakVisitors:input.expectedPeakVisitors,limits:{maxActiveAdmissionLeases:input.limits.maxActiveAdmissionLeases,admissionsPerMinute:input.limits.admissionsPerMinute,admissionTtlSeconds:input.limits.admissionTtlSeconds},totp:{mode:input.totp.mode,enabled:input.totp.enabled}};
}
export function downloadSetupReport(report){
  const url=URL.createObjectURL(new Blob([JSON.stringify(report,null,2)+'\n'],{type:'application/json'})),link=document.createElement('a');
  try{link.href=url;link.download='waiting-room-installation-result.json';document.body.append(link);link.click();}finally{link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
}
