// SPDX-License-Identifier: Apache-2.0
export const labStates={queued:'실행 대기',running:'검사 중',cancelling:'중지 중',passed:'통과',failed:'실패',cancelled:'중지됨',interrupted:'실행 끊김'};
export const labStages={starting:'시험 환경 준비',joining:'방문자 생성·재시도 검사',checking:'대기·입장 흐름 확인',finished:'검사 완료'};
export const labChecks={'join-retry':'입장 요청 재시도','fifo':'FIFO 순서','lease-cap':'입장 한도','claim-retry':'입장권 중복 발급 방지','early-claim':'대기자의 조기 입장 차단','origin-protection':'샘플 원본 보호','coordinator-loss':'Coordinator 중단 시 보호','scenario-stage':'검사 진행'};
export const activeRun=run=>['queued','running','cancelling'].includes(run?.state);
export function presetLabel(preset){return preset==='quick-20'?'Quick 20':'Smoke 1K';}
export function mergeRun(items,run){return [run,...items.filter(item=>item.id!==run.id)].sort((a,b)=>Date.parse(b.createdAt)-Date.parse(a.createdAt)).slice(0,20);}
export function downloadLab(run){const url=URL.createObjectURL(new Blob([JSON.stringify(run,null,2)+'\n'],{type:'application/json'})),a=document.createElement('a');try{a.href=url;a.download='waiting-room-traffic-lab-result.json';document.body.append(a);a.click();}finally{a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}}
