# overselect plugin

This plugin adds the `checking-selector-overlap` skill. It finds Kubernetes label selectors that match the pods of more than one workload: Services that send traffic to the wrong pods, pods covered by two PodDisruptionBudgets, and workloads whose selectors overlap. It reads rendered manifests and never contacts a cluster.

It needs Go 1.22 or newer: `scripts/run.sh` builds the tool from the bundled source each time it runs. See the repository README for usage, findings and limits.

Install it from this repository's marketplace:

    /plugin install overselect --marketplace GillMacedo/overselect
