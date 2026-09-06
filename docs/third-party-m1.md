# M1 개발 의존성 기록

공개 배포 전 전체 license/SBOM gate를 대체하지 않는다.

| 의존성 | 고정 버전 | 로컬 배포본에서 확인한 license | 역할 |
|---|---|---|---|
| github.com/valkey-io/valkey-go | v1.0.69 | Apache-2.0 | 공식 Valkey Go client |
| golang.org/x/sys | v0.31.0 | BSD-3-Clause | Go client 간접 runtime 의존성 |
| valkey/valkey | 8.1.6 + Compose image digest | container 전체 third-party inventory는 후속 | 격리 개발용 process |

Go checksum은 `go.sum`, Node 개발 도구는 `package-lock.json`에 고정한다.
Go module test-only 의존성은 `go mod tidy` 과정에서 내려받았지만 application import graph와
구분한다. production license classifier·취약점 scan·SBOM은 아직 미구현이다.
