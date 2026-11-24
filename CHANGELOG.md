# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-24

### Added
- Compare every Service, PodDisruptionBudget and workload `spec.selector` in Kubernetes manifests with every pod template, and report selectors that match the pods of more than one workload.
- Service checks follow EndpointSlice port rules: numeric target ports reach every selected pod, named ones only pods with that port name. Same-image splits, such as canaries, are reported as notes.
- Budget checks for pods covered by two PodDisruptionBudgets, which the eviction API refuses to evict, and for one budget shared by several workloads.
- Workload selector overlaps, grouped when several workloads share one selector, with the HPA that scales on that selector named.
- Files, folders, globs and stdin, `--format json` and `--fail-on`.
- Exit code 3, with no result, for empty, garbage, cut-off or failed-command input, and when nothing could be compared.
- `checking-selector-overlap` skill and plugin manifest.
