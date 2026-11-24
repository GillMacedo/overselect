# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/overselect/security/advisories/new), not in a public issue.

A case where `overselect` misses an overlap or reports one that Kubernetes doesn't have is a normal bug, unless it could lead someone to apply a selector that sends traffic or evictions to the wrong pods. If it could, report it privately. `overselect` is a diagnostic aid. It reads files and doesn't change or enforce anything.
