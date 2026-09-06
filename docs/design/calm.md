# Calm — built-in waiting-page template

Status: local implementation reference; not a production release approval.

## Concept and design decisions

[Generated concept](calm-concept.png), 1448 × 1086. The concept was adopted for
implementation in this task; it was not separately approved by the user.
Prompt summary: a quiet, white Korean waiting page, small wordmark/language switch,
one centered heading, truthful status panel, green status accent, generous whitespace,
no fabricated queue count, progress bar, countdown or decorative illustration.

| Token | Value |
|---|---|
| Background / foreground | `#ffffff` / `#152023` |
| Secondary text | `#657171` |
| Accent / border | `#12745b` / `#e1e8e5` |
| Content width | 616 CSS px, fluid with 24 px mobile margins |
| Heading | 58 px at concept-native width, 42 px medium desktop, 30 px mobile; system Korean sans-serif |
| Panel | 1 px border, 14 px radius, no shadow |
| Motion | none; reduced-motion respected |

Copy anchors: “순서를 기다리고 있어요”, “입장 상태”, “대기 중”,
“예상 대기 시간은 아직 계산 중이에요.”, “새로고침해도 순서는 유지돼요.”,
“Powered by Waiting Room”. READY: “입장할 준비가 됐어요” / “입장하기”.
Queued has no primary CTA. Ready has one explicit claim CTA. Unavailable retries
the same ticket; expired requires explicit return/rejoin. KR and EN are code text.

Components: header + language select; heading + explanation; state panel + optional
claim/retry action; refresh reassurance; footer. The generated raster is a design
reference only and is never served as the functional page.

## Template extension contract

`internal/waiting/renderer.go` owns the built-in registry. `calm` is the default.
Add an audited local stylesheet under `internal/waiting/templates/` and register a
stable ID and display name. All templates use the same semantic HTML, KR/EN copy,
queue state machine, polling/heartbeat and claim form; CSS must retain controls,
focus visibility and state clarity. Update the Admin `Theme.templateId` schema enum
and contract tests with each new registered ID. Unknown IDs fail startup; no fallback that
silently hides configuration mistakes. Template assets are compiled with `go:embed`.

Local selection: `go run ./cmd/wr-lab -template calm`. The returned registry can be
reused by Backoffice later; a Backoffice selector, persisted room choice and live
preview are **M3 TODO**, not delivered here. New layout markup or third-party
uploads are not supported yet. Arbitrary remote HTML, CSS, JavaScript, SVG and
unsanitized uploaded logos are not accepted.

Final visual comparison and browser evidence live in
[the browser/template report](../evidence/browser-template-summary.md).
