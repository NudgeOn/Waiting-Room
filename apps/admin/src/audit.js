// SPDX-License-Identifier: Apache-2.0
const actions={'lab.run':'Traffic Lab 실행·중지','lab.result':'Traffic Lab 결과','installation.apply':'설치 설정 적용','config.save_draft':'초안 저장','config.write':'설정 배포','runtime.operate':'대기열 운영','runtime.instant_off':'보호 즉시 해제','runtime.recovery':'대기열 복구','events.write':'예약 변경','events.transition':'예약 실행','users.write':'사용자 변경','security.totp.reset':'TOTP 초기화','security.totp.write':'TOTP 정책 변경','auth.reauth':'민감한 명령 재인증','auth.login':'로그인','auth.lockout':'인증 잠금','auth.logout':'로그아웃','auth.totp.enroll':'TOTP 등록','auth.totp.recover':'복구 코드 로그인'};
const results={passed:'시험 통과',failed:'시험 실패',cancelled:'시험 중지',interrupted:'실행 끊김',applied:'적용 완료',saved_draft:'저장 완료',accepted:'적용 명령 저장',rejected:'요청 거부',held:'안전 대기 시작',resumed:'검증 후 복구',validation_failed:'복구 검증 실패',recovered_before_observation:'복구 완료 뒤 관측',authenticated:'인증 완료',locked:'시도 한도 도달',logged_out:'세션 종료',enrolled:'등록 완료',challenge_required:'TOTP 확인 필요',enrollment_required:'TOTP 등록 필요'};
export function auditDisplay(event){
 const date=new Date(event.at),valid=Number.isFinite(date.getTime());
 return {action:event.action==='auth.bootstrap'?'최초 관리자 생성':actions[event.action]??'운영 기록',result:event.result==='admin_created'?'계정 생성 완료':results[event.result]??'기록 확인',dateTime:valid?date.toISOString():undefined,time:valid?date.toLocaleString('ko-KR'):'발생 시각 확인 필요'};
}
