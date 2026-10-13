# Working on kea-mcp

## Scope

This is a Go STDIO MCP server for the ISC Kea API. Keep all Kea operations behind
the API client. Do not add direct database access or require database credentials.
Support direct daemon HTTP/HTTPS APIs and the legacy Control Agent.

Read `.agents/skills/kea-mcp/SKILL.md` when changing Kea commands, transports,
configuration, MCP tools, resources, or API documentation.

## Layout

- `main.go`: configuration and STDIO lifecycle; root executable package.
- `config`: TOML file discovery and validation; no dotenv loading.
- `kea`: command envelopes, result handling, authentication, HTTP transport.
- `server`: MCP tool/resource registration and handlers.

Use go-kit `confx` for TOML decoding. Select --config, cwd/.kea-mcp.toml,
~/.agents/.kea-mcp.toml, then ~/.codex/.kea-mcp.toml; never merge sources or
fall back after a selected file fails. Require a configuration file; only an
explicit api.password_env reference reads credentials from the environment.
Require exactly one password source: password, password_env, or password_file.
Resolve relative password_file paths from the TOML directory. Use synthetic
credential files in tests; never expose credential contents in errors.
Use `logx` for stderr logs.
Do not enable HTTP body logging. Release packaging uses the pinned
`go tool buildx`; do not add local replacements for the sibling go-kit checkout.

Keep MCP protocol output on stdout and logs on stderr. Preserve tool names and
resource URIs unless a requested interface change requires otherwise.

## Compatibility

Default to Control Agent mode for existing configurations. Direct mode must bind
to a declared daemon, omit wire `service` routing, and reject other targets.
Never silently retry mutations through another mode or endpoint.
Discover commands with `list-commands` and check deployed versions and hooks;
the latest API reference does not establish older server capabilities.
Register stable command-specific tool names from the intersection of live
availability and reviewed access policy. Read-only mode hides write tools.
Keep raw_command disabled unless explicitly configured under [tools]. Recheck
availability for the selected service before dispatch; never retry mutations.
Control Agent api.services optionally restricts targets. Discovery and example
resources preserve successes alongside per-service errors. Refresh tools through
kea_discover or the commands resource; unavailable services must allow recovery.

Raw commands can mutate Kea. Describe that accurately. Configuration responses
may contain credentials; never store unredacted live responses in logs, fixtures,
documentation, or issues. Do not read or edit real secret files.

Preserve api.read_only enforcement in the shared Kea client before transport.
Maintain kea/access.go's explicit command classifications against ISC Access labels;
unknown commands must deny in read-only mode. Never infer access by command name
or widen permission from live discovery. Filter command discovery by this policy
without treating the inventory as a claim of availability on older deployments.

## Work and validation

Inspect the worktree first and preserve unrelated edits. Keep changes scoped to
the request. No commits, dependency installation, deployment, or live Kea writes
without user authorization. Use mock endpoints for routing and command tests.

Format changed Go files with `gofmt`. Run `go test ./...`, `go vet ./...`, and
available Go diagnostics. Run vulnerability checks when module changes occur;
report findings without silently expanding scope to upgrades.
Tests should prove observable behavior, especially both modes and rejection of
invalid targets. State whether validation used mocks or a live Kea deployment.
Leave generic decoding, logging, and packaging tests to go-kit's module suites.
Test this repo's configuration policy and integration with those modules; do not
repeat their format matrices or library internals.

Keep README variables and examples synchronized with configuration. Retain Go
and favor prebuilt binaries for common OS/CPU targets for broad usability.
