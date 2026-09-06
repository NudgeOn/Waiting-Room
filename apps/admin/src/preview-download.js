// SPDX-License-Identifier: Apache-2.0
// Called only from an explicit user action. Fixed filename; no server path,
// input-derived HTML, persistent browser storage or remote destination.
export function downloadReport(report){
  const blob=new Blob([JSON.stringify(report,null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob),link=document.createElement('a');
  try{link.href=url;link.download='waiting-room-planning-report.json';document.body.append(link);link.click();}
  finally{link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
}
