# ADR-0004 — v1 방문자 자격증명과 사용자 계정의 경계

상태: MAIN/SUB-PRD-01의 확정된 v1 범위를 기록. 2026-09-09 UTC.

Queue ticket과 admission은 bearer 자격증명이다. 같은 유효한 자격증명을 가진 두 요청을
동일한 대기표 또는 입장권으로 처리한다. 앱 로그인 계정, 사람 한 명 또는 물리 기기 한 대의
독점 권한으로 해석하지 않는다. account deduplication, 기기 결합과 토큰 공유 방지는 v1
비범위다. 기존 앱 OAuth와 Waiting Room admission은 각각의 목적에 맞게 별도로 검증한다.

활성 admission lease는 아직 만료되지 않은 발급 토큰 한 건이다. 온라인 사용자 수나 TCP
연결 수를 나타내지 않는다. 동일 claim의 재시도는 같은 서명 토큰과 만료 시각을 반환하며
새 lease를 만들거나 기존 토큰 수명을 연장하지 않는다.

공개 API의 출처 quota는 남용 제한이다. Gateway가 실제 연결 상대를 HMAC 처리하며,
클라이언트가 보낸 Forwarded 계열 헤더는 출처의 근거로 사용하지 않는다. 같은 NAT나
프록시를 통과하는 방문자가 quota를 공유할 수 있다. 이 제한을 사람별 인증이나
계정별 중복 방지로 설명하지 않는다.

근거: [제품 범위](../sub-prd_01.md), [공개 API 계약](../sub-prd_03.md),
[공개 제한 운영 설명](../operators/public-api-validation.md).
이 결정은 서명·epoch·audience·만료 검사를 생략하거나 admission을 origin에 전달할 권한을
추가하지 않는다. Gateway는 입장권을 검증한 뒤 기존 원본 자격증명과 분리해 처리한다.
