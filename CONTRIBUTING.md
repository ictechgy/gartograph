# Contributing

Read [AGENTS.md](AGENTS.md) for product contracts and module boundaries. Reproduce behavior with synthetic input, then propose a focused change on a topic branch.

## Local verification

Use the committed `go.sum` and the Go version in `go.mod` (CI reads it). These are the basic local checks; `.github/workflows/ci.yml` also runs `gofmt -l`, the built-binary exit-code contract (`Scripts/verify-cli-contract.sh`), Windows and macOS tests for the platform-specific paths, the release-asset helper regression and the workflow hardening check.

```sh
go build ./...
go vet ./...
test -z "$(gofmt -l .)"
Scripts/coverage.sh
Scripts/verify-cli-contract.sh
go run ./cmd/gartograph dead
go run ./cmd/gartograph rules --strict
go run ./cmd/gartograph cycles --level type --strict
go run ./cmd/gartograph cycles --level symbol --strict
```

Report checks that could not run and the relevant environment constraints. Preserve the 90% coverage gate. The `experiments/` oracles are separate modules that fetch third-party routers; run them when route harvesting changes.

## Pull requests and reports

Use Conventional Commits and explain why the behavior changes. Include the regression, commands and results, compatibility impact and observed limitations. Keep English and Korean user documentation consistent. Source validation and published installation are separate claims.

Changes to retention, identifiers, defaults or exchange contracts need evidence for affected consumers and both false-positive and missing-dependency cases. Graph results do not establish that deletion is safe.
