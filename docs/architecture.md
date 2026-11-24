# Architecture

    files, folders, globs, stdin
        --> k8s.go: read, refuse broken input, decode YAML documents and Lists
        --> model.go: workloads (pod template labels, selector, images, ports),
                      Services, PodDisruptionBudgets, HPAs; refuse objects missing
                      fields Kubernetes requires
        --> check.go: every Service, budget and workload selector against every
                      pod template in the same namespace
        --> main.go:  text or JSON, exit code

- **Pod templates** come from Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Argo Rollout, Job, CronJob and Pod. Pods and ReplicaSets with a controller owner reference are skipped, because their owner stands for them. Helm test hooks are skipped, because they exist only while `helm test` runs.
- **Selectors** are evaluated exactly: `matchLabels`, and `matchExpressions` with In, NotIn, Exists and DoesNotExist. A Service's selector is equality only, and a Service with no selector is skipped because Kubernetes manages no endpoints for it.
- **Service ports** follow EndpointSlice rules. A numeric `targetPort` reaches every selected pod. A named one reaches only pods with a container port of that name. A headless Service with no ports gives every selected pod a DNS record.
- **Intent:** workloads that run the same image repository are treated as a deliberate split for Services and budgets (ADR 0004). Selector overlaps between workloads are always reported.
- **Broken input** is refused before any comparison: empty, binary, not UTF-8, no final newline, YAML that doesn't parse, documents that aren't objects, objects missing required fields, a workload whose selector doesn't match its own template, and text that starts like an error from helm, kubectl, kustomize or make (ADR 0002).
- **Nothing compared** also exits 3: no pod template, or no selector that matches one.

Key choices are in `decisions/`.
