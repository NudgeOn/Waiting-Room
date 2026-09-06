// SPDX-License-Identifier: Apache-2.0
export async function previewRequest(kind,input){
  if(!['plan','estimate','report'].includes(kind))throw Error('지원하지 않는 미리보기입니다.');
  let response;
  try {response=await fetch('/preview/'+kind,{method:'POST',headers:{'Content-Type':'application/json','X-WR-Preview':'1'},credentials:'omit',cache:'no-store',redirect:'error',signal:AbortSignal.timeout(10000),body:JSON.stringify(input)});}
  catch {throw Error('미리보기 서버에 연결할 수 없습니다. 서버를 확인한 뒤 다시 시도하세요.');}
  if(!response.ok)throw Error(response.status===422?'입력값을 확인하세요. 프로필 한도·단가 형식·필수 항목을 검사하지 못했습니다.':'요청을 처리할 수 없습니다. 로컬 미리보기 주소를 확인하세요.');
  try {return await response.json();}catch{throw Error('미리보기 응답을 확인할 수 없습니다. 다시 시도하세요.');}
}
