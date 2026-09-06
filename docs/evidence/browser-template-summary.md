# Calm template + local browser slice — 2026-09-05

Local slice **GO**, each sub-PRD delivery and MAIN release **NO-GO**.
This is an uncommitted local implementation, not production security, capacity certification,
Backoffice delivery or independent human approval.

Final local bundle **PASS**: [report](m1-20260905-calm-template/report.json),
[environment](m1-20260905-calm-template/environment.json).
Run: 2026-09-05 06:26:35–06:27:59 UTC; reviewer: Codex (local source/log review).
Source SHA-256: `bda668acb669f4482df8a02c6a65f2eaa295feaf07dfc0080d2aaadb8701a8f8`.
Source remained unchanged during the run; all six step log hashes validated.

53 unique top-level tests passed: Go 31 + Node 17 + Chromium 5 (repeated runs excluded).
Existing randomized sub-scenarios remain 45. No test was counted as PASS via a skip.
All six steps passed: [foundation](m1-20260905-calm-template/foundation.log),
[integration](m1-20260905-calm-template/integration.log),
[restart](m1-20260905-calm-template/restart.log),
[app Quick20](m1-20260905-calm-template/quick20.log),
[browser](m1-20260905-calm-template/browser.log),
[final guard](m1-20260905-calm-template/final-guard.log).
The guard PASS means the expected rejection of eight NO-GO sub-PRDs, **not** a final integration PASS.

## Implemented and checked

- Built-in `calm` ID/registry, CLI selection, shared escaped HTML/JS and registered CSS assets.
- Native browser protected GET → cookie-backed wait → READY → explicit claim → 303 original target.
- Real Valkey refresh/ticket stability, sequential multi-tab target isolation, claim replay,
  heartbeat CSRF, invalid-return rejection, origin credential stripping, customer OAuth preservation.
- KR/EN, queued/ready/admitted/unavailable/expired presentation; no fabricated wait/count.
- Admin Theme schema accepts `templateId=calm`; actual Backoffice selector/persistence is M3 TODO.

New Go tests: renderer 3 unit + browser 3 unit + real Valkey browser 1 integration.
New Admin theme schema test: 1. Chromium end-to-end tests: 5. All passed again in the final bundle.

## Browser QA

Flow: `/shop` → queued page → READY “입장하기” → original per-tab `/shop/*?item=*`.
IAB first: actual page rendered, AX tree inspected, language switched to English.
Browser plugin availability: **Browser plugin not available** (its dedicated skill is not listed).
Regular Playwright supplies repeatable regression and exact viewport screenshots.
Environment: macOS arm64, local Go/Valkey, Playwright 1.63.0 Chromium; 1448×1086 desktop,
360×800 mobile and initial IAB 741×813. Commands: `make test-browser`, `make test-integration`.

| Check | Result | Evidence |
|---|---|---|
| Identity / nonblank / no framework overlay | PASS | correct title, path and exact main text assertions |
| Console health | PASS | no page error; queued happy path no warning/error |
| Keyboard / language | PASS | Tab focus outline, native select, KR/EN document lang |
| Refresh / native form / two tabs | PASS | same cookie; independent original targets; deterministic admission cookie |
| Mobile / reduced motion | PASS | no horizontal overflow KR/EN, 44px language target |
| Failure UI | PASS, injected | 503 retains ticket, retry recovers; 410 explicit rejoin CTA |
| Real Coordinator loss | PASS | Go integration: 503 without rejoin/origin bypass |

First browser run: 4 PASS / 1 FAIL. Native form was denied with 403 under `no-referrer`.
Changed to `strict-origin`: real same-origin Origin retained; Referer contains only origin,
never the sealed query. Added actual submitted-header assertions; next run 5/5 PASS.
This matches the [MDN referrer-policy behavior](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Referrer-Policy).
The exact Origin + ticket-bound AEAD check was not relaxed.

## Fidelity ledger

Reference: [concept PNG](../design/calm-concept.png), [tokens/copy/extension brief](../design/calm.md).
Reference and latest rendered screenshots were inspected together with `view_image`,
including the reference-native 1448×1086 render and 360×800 mobile render.

| Comparison | Concept → rendered evidence | Action / outcome |
|---|---|---|
| Copy/order | wordmark, title, two description lines, state, estimate, reassurance, footer | exact queued copy anchors retained; no added visible marketing copy |
| Layout | one centered 738px panel at x=355, top near479 | corrected first render's lower panel position |
| Typography | large Korean heading and readable labels | desktop heading 50→58px, supporting copy 20→24px, footer 14→20px; platform font rasterization remains intentional |
| Header control | outlined language select | restored outline/62px desktop target; native system chevron is intentional for keyboard/mobile select |
| Palette/border | white/charcoal/green, thin gray panel | retained palette, corrected panel/divider border contrast |
| Spacing/shape | 14px panel radius, generous internal whitespace | corrected 194px-tall first panel to approximately232px (concept approximately236px) |
| Asset treatment | no hero illustration | generated raster is reference only, all text/controls are functional HTML |
| Responsive/motion | desktop-only concept | intentional 360px adaptation; no animation, no clipped copy or horizontal overflow |

Above-fold copy diff: no added/removed/renamed/reordered queued content; KR/EN menu options
and READY/error copy follow the design brief. Core composition faithfully verified against the
adopted concept; no material visual mismatch remains. This is not a pixel-identical font claim.

## Remaining boundaries

- All cookies here are loopback HTTP `wr_dev_*`, not production Secure `__Host-*` certification.
- Ephemeral one-process return key; no distribution/rotation, durable keys or signed config.
- Lost initial join response and simultaneous cookie-less first tabs can allocate duplicate tickets.
- No server-side early-poll enforcement, full return fuzz suite, browser Quick20 or 10K/100K test.
- Safari/Firefox, production TLS, screen-reader audit, 200% zoom and full WCAG AA are not verified.
- No arbitrary uploaded template/HTML/CSS/JS; no Backoffice selector, preview, save or TOTP UI yet.
- Developer Ajv was updated from 8.17.1 to 8.20.0 after its audit warning; install audit reported 0 vulnerabilities.

Final screenshots (local temporary artifacts, not production images):
[desktop](/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-calm-a2j8uw/desktop.png),
[mobile](/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-calm-a2j8uw/mobile.png),
[ready](/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-calm-a2j8uw/ready.png),
[expired](/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-calm-a2j8uw/expired.png).
Final reference/render `view_image` comparison was repeated after this exact bundle completed.
Lab HTTP processes and the dedicated Compose container were stopped; named volume/data preserved.
No commit, push, external CI, publication or deployment was performed.

Go and browser tests are evidence for this local slice only; final tests remain MAIN-owned and NOT RUN.
