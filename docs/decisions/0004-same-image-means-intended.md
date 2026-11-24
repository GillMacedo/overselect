# 0004: Same image repository means a deliberate split

**Decision:** when a Service or a PodDisruptionBudget selects the pods of several workloads whose serving containers all run the same image repository (any tag), report a note (OS103) for a Service and nothing for a budget. When the images differ, report an error (OS101) or a warning (OS202).

**What is compared:** for a Service port, the image of the container that declares the target port, or the first container when none does. For a budget, the first container. Sidecars are never compared, so two programs that share a sidecar image are still reported.

**Why:** Kubernetes documents canary deployments that share a Service, and clustered software (Redis replicas, Tempo's gossip ring, ClickHouse shards) uses one headless Service over several StatefulSets or Deployments of the same image. On the real charts listed in the README, this rule kept those quiet and still caught a Service shared by an application and a mail sink. Workload selectors that overlap are reported whatever the images, because the Kubernetes docs warn against it in every case.

**Limit:** two different programs built into one image, selected by one Service, are not reported. The README says so.
