# 0001: A skill with bundled Go source, not an MCP server

**Decision:** ship a Claude Code skill whose `run.sh` builds the Go source in `plugin/scripts` and runs it.

**Why:** the agent needs one read-only check over files it can already read. A skill with a script gives it that without a server process, and the same binary runs in CI and by hand. Go is the language of the Kubernetes tooling the users already have, and the standard library plus a copied YAML parser is enough.

**Rule:** no network, no cluster access, no dependencies outside the standard library and the copied parser.
