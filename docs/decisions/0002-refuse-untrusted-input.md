# 0002: Refuse input that can't be trusted, and warn when a selector matches nothing

**Decision:** exit 3 with no result for empty, garbage, cut-off or failed-command input, and for input with nothing to compare. A selector that matches no pod template is a warning (OS901), which fails under the default `--fail-on warn`.

**Why:** a tool that checks for overlap reports "no overlap" when pods are missing. A render that failed, a file cut off, or a release split over files you didn't pass would all look clean. Required fields catch a file cut inside an object. A file cut between two objects leaves valid objects, and the usual sign is a Service or budget whose pods are gone, because Helm writes Services and budgets before workloads. `TestEveryLineCut` checks that no cut of the example prints a clean result.

**Cost:** charts whose Services select pods an operator creates exit 1 by default. Users pass `--fail-on error` for those.
