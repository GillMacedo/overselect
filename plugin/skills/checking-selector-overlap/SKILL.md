---
name: checking-selector-overlap
description: Checks Kubernetes manifests and rendered Helm charts for label selectors that match the pods of more than one workload. Finds Services that send some requests to another workload's pods (intermittent 502s or connection refused), pods covered by two PodDisruptionBudgets (kubectl drain stuck on "more than one PodDisruptionBudget"), one budget shared by several workloads, and Deployment or StatefulSet selectors that overlap. Use when the user sees a Service with unexpected endpoints, a node drain that won't evict a pod, an HPA reacting to the wrong pods, or wants a chart's selectors reviewed before release.
---

# Checking selector overlap

A selector matches every pod that has all of its labels. Extra labels on a pod don't exclude it, so a Service or budget that selects only `app.kubernetes.io/name` and `app.kubernetes.io/instance`, as `helm create` scaffolds it, also matches every other component of the release that carries those two labels.

## Run the tool

It needs Go 1.22 or newer. It only reads files and never contacts a cluster.

    sh "${CLAUDE_PLUGIN_ROOT}/scripts/run.sh" MANIFEST_FILE_OR_DIR...

- For a Helm chart, render it first, with the values the user deploys: `helm template RELEASE ./chart -f values.yaml > /tmp/rendered.yaml`, then pass that file. Rendering doesn't contact a cluster, but ask before running anything that does, such as `kubectl get`.
- For kustomize, run `kustomize build DIR > /tmp/rendered.yaml` first.
- Pass every file the release deploys. A selector whose pods are in a file you left out is reported as OS901.
- `-` reads from stdin. Add `--format json` to read the result in detail.

Exit codes: 0 means no overlap, 1 means findings, 2 means bad options, and 3 means the input can't be trusted or nothing could be compared. With exit 3 there is no result, so never report the manifests as fine.

## Read the result

- `OS101`: a Service spreads requests across pods of workloads that run different images. Requests that land on the wrong pods fail or get the wrong answer. This is the most urgent one.
- `OS102`: a Service also selects a Job's pods, such as a migration hook, while they run.
- `OS103` (note): several workloads with the same image share a Service. That is usually on purpose, as in a canary split or a cluster's headless Service. Mention it, but don't call it a bug.
- `OS104`: a Service's named target port matches no container port, so that port has no endpoints.
- `OS201`: pods covered by two budgets can't be evicted at all, so drains and node upgrades stall on them.
- `OS202`: one budget counts several workloads, so it can let every pod of one of them go.
- `OS301`: a workload's `spec.selector` matches another workload's pods. `OS302` is the same for a Job's pods or a bare pod, reported when an HPA reads that selector.
- `OS901`: a selector matched no pod template in the files. Either the pods come from an operator or a file you didn't pass, or the input is incomplete. Find out which before saying anything is fine.

## Report

1. For each finding, give the object, its `file:line`, the selector, and the other workload whose pods it matches.
2. Propose the smallest fix: add a label that only the intended pods carry, usually `app.kubernetes.io/component`, to the Service or budget selector. The tool suggests one when it can. For a chart, make the fix in the template or helper that renders the selector.
3. Warn that `spec.selector` of a Deployment, StatefulSet or DaemonSet can't be changed in place: the workload has to be recreated. Changing a Service or budget selector is safe to apply.
4. Say what wasn't checked: pods created by operators, objects in other files, and live cluster state.

## Don't

Don't edit files unless the user asks. Don't call the manifests safe when the tool exited 3 or reported OS901.
