# overselect: find Kubernetes selectors that match the wrong pods

[![CI](https://github.com/GillMacedo/overselect/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/overselect/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI that reads Kubernetes manifests or `helm template` output and finds Services, PodDisruptionBudgets and Deployment or StatefulSet selectors that match the pods of more than one workload.**

A label selector matches every pod that has all of its labels, and extra labels don't exclude a pod. `helm create` scaffolds a `selectorLabels` helper with only `app.kubernetes.io/name` and `app.kubernetes.io/instance`. When a chart adds a second component that reuses those two labels, the main Service also sends requests to the new pods, the main PodDisruptionBudget counts them, and the main Deployment's selector matches them. Kubernetes accepts all of it. You find out from intermittent 502s, from `kubectl drain` stuck on "This pod has more than one PodDisruptionBudget", or from an HPA reacting to the wrong pods.

`overselect` compares every selector in your manifests with every pod template, offline, before you deploy. kube-linter and kube-score report a selector that matches nothing. overselect reports one that matches too much.

## What you can ask Claude

- "Some requests to our Service return 502 and others work. Is the selector picking up other pods?"
- "kubectl drain says a pod has more than one PodDisruptionBudget. Which ones?"
- "Check this Helm chart's selectors before we release it."
- "We added a worker Deployment to the chart. Does anything else select its pods now?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/overselect
    claude plugin install overselect@overselect-marketplace

The skill builds the tool from source with Go 1.22 or newer. It has no dependencies. To run it yourself, clone the repo and use `go run ./plugin/scripts`, or `sh plugin/scripts/run.sh` from any folder.

## Example

`examples/shared-labels` is a chart scaffolded with `helm create`, rendered after adding a mail sink for test environments. The sink reuses the scaffold's two selector labels and adds `app.kubernetes.io/component: mailsink`:

    $ go run ./plugin/scripts --fail-on never examples/shared-labels
    overselect: 2 Services, 2 PodDisruptionBudgets and 2 workload selectors checked against 2 pod templates

    ERROR OS101 examples/shared-labels/rendered.yaml:7  Service demo-api sends traffic to pods of 2 workloads that run different images: Deployment demo-api, Deployment demo-api-mailsink
      port 80 -> 8080:
        Deployment demo-api (ghcr.io/example/api) declares 8080
        Deployment demo-api-mailsink (axllent/mailpit) doesn't declare 8080, so connections to its pods are likely refused
      Selector app.kubernetes.io/instance=demo, app.kubernetes.io/name=api matches all of their pod templates, so the Service spreads requests across their pods.
      Fix: If the Service is meant for Deployment demo-api, add app.kubernetes.io/component=server to its selector. Only that pod template has it.

    ERROR OS301 examples/shared-labels/rendered.yaml:63  spec.selector of Deployment demo-api also matches the pods of Deployment demo-api-mailsink
      Selector app.kubernetes.io/instance=demo, app.kubernetes.io/name=api matches the pod template of Deployment demo-api-mailsink (examples/shared-labels/rendered.yaml:90).
      Kubernetes doesn't stop overlapping selectors, and its docs warn that controllers with overlapping selectors can conflict and behave unexpectedly. Commands that find pods by the selector, such as kubectl logs deployment/NAME and kubectl expose, can pick the other pods.
      HorizontalPodAutoscaler demo-api scales Deployment demo-api on the metrics of every pod its selector matches, so the pods of Deployment demo-api-mailsink count too.
      Fix: Give each workload a label value the others don't share, such as app.kubernetes.io/component, in its pod template and its selector. spec.selector can't be changed in place, so the workload has to be recreated, for example with kubectl delete --cascade=orphan and then apply.

    ERROR OS201 examples/shared-labels/rendered.yaml:90  Pods of Deployment demo-api-mailsink are covered by 2 PodDisruptionBudgets: demo-api, demo-api-mailsink
      The eviction API refuses to evict a pod covered by more than one budget ("This pod has more than one PodDisruptionBudget, which the eviction subresource does not support"), so kubectl drain and node upgrades that evict through it can't move these pods.
      PodDisruptionBudget demo-api (examples/shared-labels/rendered.yaml:42): selector app.kubernetes.io/instance=demo, app.kubernetes.io/name=api
      PodDisruptionBudget demo-api-mailsink (examples/shared-labels/rendered.yaml:53): selector app.kubernetes.io/component=mailsink
      Fix: Keep one budget for these pods, or narrow the selectors so each pod matches only one.

    WARN OS202 examples/shared-labels/rendered.yaml:42  PodDisruptionBudget demo-api counts the pods of 2 workloads as one budget: Deployment demo-api, Deployment demo-api-mailsink
      Selector app.kubernetes.io/instance=demo, app.kubernetes.io/name=api matches all of their pod templates.
      With minAvailable: 1, pods of one workload can keep the budget met while every pod of another is evicted.
      Fix: Use one PodDisruptionBudget per workload, with a selector that matches only that workload's pods.

    Result: 4 findings where a selector matches pods it probably shouldn't.
    overselect reads manifests only. Pods created by operators or defined in other files aren't seen.

`--fail-on never` is here only so the README's example exits 0. Without it, findings exit 1.

`examples/healthy` is the same release with a component label in every selector, a worker that runs the same image under its own selector, and a migration Job hook whose pods no selector matches:

    $ go run ./plugin/scripts examples/healthy
    overselect: 1 Service, 1 PodDisruptionBudget and 2 workload selectors checked against 3 pod templates

    Result: every selector checked matches the pods of one workload.
    overselect reads manifests only. Pods created by operators or defined in other files aren't seen.

On a real chart, `tests/testdata/apexkube-agent-2.2.0.yaml` is Improwised's apexkube-agent chart 2.2.0 rendered with both of its agents on. It finds the overlap reported in Improwised/charts issue 159:

    $ go run ./plugin/scripts --fail-on never tests/testdata/apexkube-agent-2.2.0.yaml
    overselect: 1 Service, 0 PodDisruptionBudgets and 2 workload selectors checked against 2 pod templates

    ERROR OS301 tests/testdata/apexkube-agent-2.2.0.yaml:30  spec.selector of Deployment apexkube-agent also matches the pods of Deployment apexkube-agent-metrics
      Selector app.kubernetes.io/instance=apexkube-agent, app.kubernetes.io/name=apexkube-agent matches the pod template of Deployment apexkube-agent-metrics (tests/testdata/apexkube-agent-2.2.0.yaml:119).
      Kubernetes doesn't stop overlapping selectors, and its docs warn that controllers with overlapping selectors can conflict and behave unexpectedly. Commands that find pods by the selector, such as kubectl logs deployment/NAME and kubectl expose, can pick the other pods.
      Fix: Give each workload a label value the others don't share, such as app.kubernetes.io/component, in its pod template and its selector. spec.selector can't be changed in place, so the workload has to be recreated, for example with kubectl delete --cascade=orphan and then apply.

    Result: 1 finding where a selector matches pods it probably shouldn't.
    overselect reads manifests only. Pods created by operators or defined in other files aren't seen.

## Usage

    overselect [--format text|json] [--fail-on warn|error|never] MANIFEST...

- `MANIFEST` is a file, a folder (its `.yaml`, `.yml` and `.json` files, recursively, skipping hidden folders), a glob, or `-` for stdin. Pass every file the release deploys.
- For Helm, render first: `helm template my-release ./chart -f values.yaml | overselect -`. For kustomize: `kustomize build overlays/prod | overselect -`. `kubectl get deploy,sts,svc,pdb -o yaml` output works too; pods and ReplicaSets owned by a controller are skipped.
- Objects without a namespace are treated as being in the same namespace as every other object.

| Exit code | Meaning |
|---|---|
| 0 | No finding at the `--fail-on` level, or `--fail-on never` |
| 1 | Findings at the `--fail-on` level: `warn` by default |
| 2 | Bad options |
| 3 | The input can't be trusted, or nothing could be compared. No result is printed, whatever `--fail-on` says |

Exit 3 covers empty files, binary or random text, YAML that doesn't parse, files with no final newline, objects missing fields Kubernetes requires (such as containers, `spec.selector`, Service ports or `maxReplicas`), a workload whose selector doesn't match its own pod template, files that start like output from a failed `helm`, `kubectl`, `kustomize` or `make`, and manifests with no pod template or no selector that matches one. `go run` turns any failing exit code into 1, so build the binary when you need the exact code.

## Findings

| Code | Level | Meaning |
|---|---|---|
| OS101 | error | A Service sends traffic to pods of workloads that run different images |
| OS102 | warn | A Service also sends traffic to a Job's or CronJob's pods while they run |
| OS103 | note | A Service sends traffic to pods of workloads that run the same image, as in a canary split or a cluster's headless Service. Doesn't fail |
| OS104 | warn | A Service's named `targetPort` matches no container port in the pods it selects, so that port has no endpoints |
| OS201 | error | A workload's pods are covered by more than one PodDisruptionBudget, so the eviction API refuses to evict them |
| OS202 | warn | One PodDisruptionBudget counts the pods of several workloads that run different images |
| OS301 | error | A workload's `spec.selector` matches another workload's pod template |
| OS302 | warn | A Deployment's selector matches a Job's or a bare pod's template, and an HPA scales on that selector |
| OS901 | warn | A selector matches no pod template in the files, so it wasn't compared. Its pods come from an operator or another file, or the input is incomplete |

How a Service port is matched follows the EndpointSlice rules: a numeric `targetPort` sends traffic to every selected pod, and a named one only to pods with a container port of that name. A pod without the name is left out, so it isn't reported. When the Service is meant for one workload, chosen by name or by being the only one that declares the port, the fix suggests a label only that workload's pods carry.

## How it compares

[kube-linter](https://github.com/stackrox/kube-linter) (`dangling-service`, `mismatching-selector`) and [kube-score](https://github.com/zegl/kube-score) (`service-targets-pod`, `deployment-has-poddisruptionbudget`) check that a selector matches something and that a Deployment's selector matches its own template. Neither checks whether a selector also matches the pods of another workload. Tools that find overlapping budgets or selectors exist for live clusters, such as pre-drain checkers, and need cluster access. overselect works on files, so it runs in pull request CI and on a laptop before anything is deployed.

## Limits

- **Files only.** Pods created by operators, such as CloudNativePG clusters or the Prometheus Operator's StatefulSets, aren't in the manifests, so selectors for them are reported as OS901. Use `--fail-on error` if that is expected. Custom resources with pod templates other than Argo Rollouts aren't read.
- **A file cut between two complete objects can't be detected.** overselect refuses a file cut inside an object, because required fields are missing. If what is left is still complete, valid objects, the result covers only those. Helm sorts Services and PodDisruptionBudgets before workloads, so a Helm file cut that way usually leaves selectors that match nothing, which OS901 reports.
- **"Same image" is a guess about intent.** Two workloads whose serving container runs the same image repository, at any tag, are treated as a deliberate split for a Service (OS103). For a budget, the first container of each is compared. So two different programs built into one image and selected by one Service aren't reported. A selector overlap between workloads (OS301) is reported whatever the images.
- **Ports a container listens on without declaring them** are reported as "likely refused", because the manifests can't show them.
- **Namespaces.** An object without a namespace is compared with every namespace, so files that mix explicit namespaces with none can show overlaps that don't exist once applied.
- **Not read:** NetworkPolicy, ServiceMonitor and PodMonitor selectors, topology spread and affinity selectors, and Gateway or Ingress routing.

## FAQ

**Why does my Service send traffic to the wrong pods?** Its selector matches every pod that carries all of the selector's labels, including pods of other workloads in the same namespace that share those labels. Check with `kubectl get endpointslices -l kubernetes.io/service-name=NAME`, and fix it by adding a label only the intended pods have, such as `app.kubernetes.io/component`.

**How do I fix "This pod has more than one PodDisruptionBudget, which the eviction subresource does not support"?** Two budgets' selectors match the same pod. Remove one or narrow the selectors so each pod matches only one. overselect lists both budgets and their selectors.

**Can I change a Deployment's selector?** Not in place: `spec.selector` is immutable in `apps/v1`. Delete the Deployment with `kubectl delete --cascade=orphan` and apply the new one, or leave the selector and change the other workload's labels so they no longer match. A Service's or budget's selector can be changed at any time.

**Does it need cluster access?** No. It reads files and never contacts a cluster or runs `helm` itself.

**Is it an MCP server?** No. It's a skill with a Go package and a script that builds it, so Go is all it needs.

## Tested on

- Unit tests for selector matching (`matchLabels` and every `matchExpressions` operator), image comparison, each finding and the cases that must not produce one, namespaces, Lists, JSON output, stdin, owned and Helm test pods, and every refused input. Integration tests build the binary and run it on both examples, on three real charts, on the shared-labels example cut after every line (no cut may print a clean result or exit 0), on the healthy example cut inside each kind of object, and on broken input in one file with the other file real: empty, whitespace, binary, random text, a YAML scalar, cut in half, cut before the last newline, failed helm, kubectl, kustomize and make output, and helm warnings mixed into the YAML. Each must exit 3 with no result, with and without `--fail-on never`. Tests run on Go 1.22.12 and Go 1.27.4, with and without `-race`.
- Real charts, rendered with Helm v3.19.0 on 2026-11-24. Findings that match an open bug report: NrgXnat XNAT chart 5.0.2 (current main) with `mailpit.enabled=true`, where the main, headless and DICOM Services and the StatefulSet selector all match the mailpit pod (their issue 41 reports 502s on about every other request); kestra chart 1.3.30 with two worker groups, where three Deployments share one selector (kestra issue 18100); Improwised apexkube-agent 2.2.0 with both agents (their issue 159). It also found that grafana's tempo-distributed chart 1.61.3 renders a memcached Service port `http-metrics` that no container declares when the exporter is off (OS104).
- Exit 0 with no finding: ingress-nginx, podinfo, cert-manager, argo-cd, argo-workflows, prometheus, grafana, consul, traefik, metrics-server, opentelemetry-collector, jenkins, harbor, minio, apache airflow, dagster, and bitnami redis, mongodb, keycloak, mysql, airflow, gitea, wordpress, nginx, elasticsearch, rabbitmq and clickhouse. redis, clickhouse and tempo-distributed print OS103 notes for headless or gossip Services shared by same-image workloads.
- OS901 only, each checked by hand and correct for the files: kube-prometheus-stack (pods the operator creates), vault in HA mode (the standby Service selects `vault-active`, a label Vault sets on pods at run time), bitnami kafka and postgresql-ha (a budget rendered for a component that is off), and bitnami mastodon (a web Service rendered without its Deployment).
- Two renders failed (loki and temporal with the values I passed) and left empty files, which overselect refused with exit 3.
- Only the Improwised, podinfo and cert-manager renders are kept in `tests/testdata`, because their licenses allow it. The others were run by hand.
- Not tested: a live cluster, real `kustomize build` output, and real `kubectl get -o yaml` output (Lists and owned pods are covered by unit tests with made-up objects). The consequences in the messages come from the Kubernetes documentation (overlapping controller selectors, the eviction API and multiple budgets, how an HPA selects pods) and the bug reports above, not from reproducing them. It hasn't been tested in a fresh Claude session.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/overselect/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `overselect` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
