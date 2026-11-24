# overselect

Claude Code skill and Go CLI that reads Kubernetes manifests and reports label selectors (Services, PodDisruptionBudgets, workload `spec.selector`) that match the pods of more than one workload.

## Architecture
All code is in `plugin/scripts`, one `package main` with no dependencies, so `go run` and `run.sh` need no setup. `k8s.go` reads files and refuses broken input, `model.go` turns objects into workloads, Services, budgets and selectors and checks required fields, `check.go` compares every selector with every pod template and builds findings, and `main.go` has flags, output and exit codes. The `yaml_*.go` files are a renamed copy of go.yaml.in/yaml/v3: don't edit them. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet, unit tests and integration tests in `tests/`, with `-race`
- `make lint`: plugin and marketplace validation
- `make demo`: run the examples

## Conventions
- Standard library only, plus the copied YAML parser. Never contact a cluster and never run helm or kubectl from the tool.
- Input that can't be trusted (empty, binary, cut off, failed command output, nothing to compare) exits 3 with no result. Never print a clean result unless at least one selector was really compared with a pod template.
- A selector that matches nothing is a warning (OS901), because it can mean the files are incomplete.
- Each finding has a stable `OSnnn` code. Don't reuse codes.
- A claim about what Kubernetes does must come from its documentation or source, linked in the pull request.
- Tests must not assume the machine they run on.

## Do and don't
- Do add a test for what a change flags and for what it must not flag, and keep `TestEveryLineCut` passing.
- Don't guess intent beyond the documented same-image rule. Report what the selector matches.

## Definition of done
`make test`, `make lint` and `make demo` pass, and `README.md`, `ROADMAP.md` and `CHANGELOG.md` are updated when relevant.
