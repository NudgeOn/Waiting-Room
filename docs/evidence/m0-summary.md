# M0 기반 구현 검증 — 2026-09-05

판정: **M0 기반 구현 GO / M1 착수 GO / 모든 sub-PRD delivery와 MAIN 출시 NO-GO**.

실행: `make check` 로컬 PASS. Go 12개, 도구 9개, OpenAPI schema contract 6개,
총 27개 테스트 PASS, 실패·skip 0. Go에는 `-race`와 40 seeds × 400 commands의
randomized reference-model trace가 포함된다. 두 OpenAPI 린트, gofmt, go vet,
9개 PRD ownership/dependency/link/ignore 검사도 PASS했다.

재현용 bundle 생성: `node scripts/run-m0.mjs m0-20260905-foundation`.
최종 bundle 재실행 **PASS**: [report.json](m0-20260905-foundation/report.json),
[실행 환경](m0-20260905-foundation/environment.json).
실행 시각: 2026-09-05 04:50:07~04:50:10 UTC (macOS arm64).
8개 검증 단계가 모두 PASS했고 소스는 실행 전후 동일했다.

- source SHA-256: `c429b9892b695e6bd5bd5e2e967e1cb2482d0e7d1e5dca849d113f874c295683`
- [Go race/model 로그](m0-20260905-foundation/go-race.log): 상위 테스트 12개 + randomized subtest 40개 PASS
- [도구 테스트](m0-20260905-foundation/tooling-unit.log): 9개 PASS
- [API 계약 테스트](m0-20260905-foundation/contract.log): 6개 PASS
- [최종 진입 guard](m0-20260905-foundation/final-guard.log): 8개 sub-PRD NO-GO를 이유로 의도한 exit 2; 최종 통합 시험은 NOT RUN

Source digest는 Git에서 ignored되지 않은 파일 경로·내용을 포함하되 `docs/evidence/`는
제외해 순환 해시를 피한다. 로그 해시는 report가 별도로 가진다. 환경 manifest는 로컬
runner가 관측한 정보이며 외부 서명·배포 attestation을 의미하지 않는다.

## 검증 범위

| 영역 | 증거 | 한계 |
|---|---|---|
| Policy | `TestCapabilityRegistry`, `TestFIFOEligibility` | FIFO registry/정렬만 구현, 제품 모드/route 미구현 |
| Queue | `internal/queue/model/model_test.go` 10개 | 단일 스레드 기준 모델; Valkey, 분산 race, persistent state 미검증 |
| Public/Admin | `test/contract/openapi.test.mjs` 6개와 린트 | schema/구조만 검증; HTTP handler, TOTP, RBAC runtime 미구현 |
| Evidence/gate | `scripts/prd.test.mjs` 9개 | M0 schema/hash/의존 gate; image/qualification/release pipeline 미구현 |
| 부하 | profile 산술 검증 | 10K/100K 성능 시험 미실행, badge 없음 |
| CI | `.github/workflows/m0.yml` 작성 | GitHub 실행 미확인 |

## 남은 조건

- Quick 20 로컬 서버·Gateway·Valkey 구현과 model trace 비교는 M1에서 수행한다.
- TOTP, 관리자 UI, 설치 wizard, 실제 앱/browser E2E는 후속 milestone이다.
- 최종 시험은 MAIN에서만 수행한다. `make test-final`은 현재 진입을 거부해야 한다.
- commit/remote/push/배포는 없다. 현재 증거는 commit 대신 uncommitted source SHA-256로 식별한다.
- support matrix는 정확한 시험 후보 버전이며 실제 store/cluster 호환성·취약점 검증은 남아 있다.
- 비공개 security/conduct 연락처 선택은 공개 전 필수다.
