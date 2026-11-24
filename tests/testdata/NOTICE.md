# Test data sources

These files are `helm template` output from public charts, rendered on 2026-11-24 with Helm v3.19.0. Only the Service, Deployment, StatefulSet, DaemonSet, ReplicaSet, PodDisruptionBudget, HorizontalPodAutoscaler, Job, CronJob and Pod documents were kept; Secrets, ConfigMaps and RBAC objects were dropped. The kept documents are unchanged.

| File | Chart | Command | License |
|---|---|---|---|
| `apexkube-agent-2.2.0.yaml` | Improwised/charts `charts/apexkube-agent` at commit d667048 (chart 2.2.0) | `helm template apexkube-agent . -f ci/ci-metrics-values.yaml --set access.enabled=true` | MIT, Copyright (c) 2022 Improwised Technologies Pvt. Ltd. See `LICENSE-improwised-charts`. |
| `podinfo.yaml` | stefanprodan/podinfo chart 6.17.0 | `helm template r podinfo/podinfo --set hpa.enabled=true` | Apache License 2.0 |
| `cert-manager.yaml` | cert-manager chart v1.21.3 | `helm template r jetstack/cert-manager --set crds.enabled=false` | Apache License 2.0 |

The apexkube-agent render has the overlap reported in Improwised/charts issue 159. The issue is still open; chart 3.0.0 with the same values renders a single Deployment with no overlap. podinfo and cert-manager have none and must stay quiet.
