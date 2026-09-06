# Admin browser dependencies

Project code remains Apache-2.0. Exact resolved versions are in package-lock.json.
Runtime: React/React DOM 19.2.8, scheduler 0.27.0, qrcode 1.5.4, dijkstrajs 1.0.3 (MIT).
Their unmodified package license texts are shipped in the static browser build as
`/assets/third-party.txt`, sourced from `apps/admin/public/assets/third-party.txt`.
Vite 8.2.2 is a development build tool. qrcode's Node/CLI dependencies (including pngjs
and yargs) appear in the installation lock but are not called by the browser QR flow.

The QR library is dynamically loaded only for enrollment, without any remote service.
No production release/SBOM or full transitive supply-chain qualification is claimed.
Before release, audit the exact shipped bundle, licenses and package-lock against SUB-PRD-08.
