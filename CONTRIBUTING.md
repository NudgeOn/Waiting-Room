# Contributing

Read [main-prd.md](docs/main-prd.md), then the relevant sub-PRD and its dependencies.
Keep implementation changes and their checklist/test evidence in the same review.

Run `npm ci --ignore-scripts` and `make check` before submitting.
Add tests for changed behavior, especially queue invariants and failure boundaries.
Never store credentials, real tokens, production traces or personal information in tests.

Contributions are licensed under Apache-2.0. Every commit must include a
`Signed-off-by: Your Name <your-email>` trailer, normally created with `git commit -s`.
Sign only when you can certify the [Developer Certificate of Origin 1.1](https://developercertificate.org/).
Do not sign on behalf of another contributor.

Only change a sub-PRD to delivery GO after its required tests, dependency gates and
review evidence are complete. M0 test success is not a release approval.
