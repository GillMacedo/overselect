# Roadmap

## Next
- NetworkPolicy `podSelector` that matches pods of workloads it wasn't written for
- ServiceMonitor and PodMonitor selectors against Services and pods
- `--namespace` to place objects that have none, instead of comparing them with every namespace

## Later
- Pod templates in more custom resources, such as Knative Services and KEDA ScaledJobs
- A GitHub Action that renders a chart with several values files and runs overselect on each
- Comparing two renders, to report a selector change before it ships
