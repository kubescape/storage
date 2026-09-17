# Contributing

The Kubescape project manages this document in the central project repository.

Go to the [centralized CONTRIBUTING.md](https://github.com/kubescape/project-governance/blob/main/CONTRIBUTING.md)

## Storage hot-path changes

A PR touching `pkg/registry/file/{storage,singlewriter,containerprofile_*,sqlite}.go`
must carry two numbers (see `docs/features/storage-measurement-harness.md`):

- **Work budgets (Tier A).** `go test ./...` includes `TestWorkBudget`. If the
  golden `pkg/registry/file/testdata/workbudget.golden.json` changes, the
  commit message states each row's delta and why; the reviewer checks the
  attribution.
- **Paired A/B (Tier B).** Run `make perf-ab` on a quiet machine and quote its
  verdict line verbatim in the PR description. `INCONCLUSIVE` and
  `UNDERPOWERED` are not `PASS`.
