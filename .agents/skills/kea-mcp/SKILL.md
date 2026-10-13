---
name: kea-mcp
description: Work on this Go Kea MCP repository when changing Kea API commands, HTTP transports, authentication, TOML configuration and credential references, MCP tools or resources, tests, or compatibility documentation. Preserve direct daemon and legacy Control Agent support and keep Kea access API-only.
---

# Kea MCP development

## Establish scope

Read root AGENTS.md and inspect the worktree. Locate the responsible package:
`config` for configuration loading, `kea` for wire behavior,
`server` for MCP exposure, and root `main.go` for STDIO lifecycle.
Preserve unrelated changes. Never add a database client or database credentials.

## Confirm the API contract

Use official sources:

- [API sockets and migration](https://kb.isc.org/docs/kea-api-sockets)
- [API reference](https://kea.readthedocs.io/en/latest/api.html)
- [Control channel protocol](https://kea.readthedocs.io/en/latest/arm/ctrl-channel.html)

Establish the deployed Kea version, target daemon, transport mode, and hooks.
Use the matching manual version; latest can include development commands.
Query `list-commands` before relying on a command; the static access policy is
not a universal availability inventory. Maintain kea/access.go against ISC's
explicit Access labels. api.read_only permits exact reviewed read commands only,
rejects writes and unknown commands before transport, and filters discovery.
Do not infer permission from command names or live availability. Preserve this
policy for every tool and resource in both modes.

Control Agent requests include a `service` array for daemon routing; agent
commands omit it. Direct mode uses a declared daemon endpoint, omits wire routing,
and rejects other targets. Use separate MCP instances for separate daemons.
Never probe using mutations or silently fall back after failures.
HA listeners are not general API endpoints.

HTTP responses use an array envelope, including direct daemon responses.
Check HTTP status and Kea results: 0 success; 1 failure; 2 unsupported;
3 empty/no affected entities; 4 conflict; 5 failed rollback after partial apply.
Interpret results by command and deployed version. HTTP 200 alone is not success.
Preserve context cancellation and bounded timeouts.

Arguments vary by command. Preserve object, array, and scalar JSON arguments;
Kea validates the deployed command's schema. Dedicated tools bind a single
command and use stable kea_ names with hyphens replaced by underscores. Expose
only reviewed commands discovered live; hide write tools in read-only mode.
Raw access requires [tools] raw_command=true and cannot bypass read-only policy.
Recheck availability on the selected service before execution. Refresh discovery
and tools through kea_discover or the commands resource. Preserve successful
results alongside per-service errors, and honor optional Control Agent api.services.

## Handle operational intent

Raw commands include configuration changes, lease mutations, and shutdown.
Require operator intent before live mutations. `config-test` does not guarantee
`config-set` success; `config-set` changes running state, while `config-write`
persists it. Avoid retries without establishing a mutation's outcome.
`config-get` can expose secrets. Use synthetic fixtures and redact live evidence
before storing or sharing it. Do not read real environment or PEM files.

## Implement and verify

Keep protocol logic in `kea`, configuration in `config`, and
MCP registration in `server`. Preserve both modes and the compatibility
default. Keep stdout reserved for MCP; diagnostics go to stderr.
Use the pinned go-kit modules: `confx` for TOML decoding, `logx` for stderr
logging, and `go tool buildx` for release archives. Select --config, then
.kea-mcp.toml in cwd, ~/.agents/, and ~/.codex/ in order. Never merge sources or
fall back after a selected file fails. Require a configuration file. Preserve
explicit password_env and password_file references, credential presence checks,
and whitespace normalization. Require exactly one password source and resolve
relative password_file paths from the TOML directory. Test with synthetic files.
Do not load dotenv files, enable HTTP body logging, or introduce local module replacements.
Update README alongside behavior changes.

Mock HTTP endpoints to inspect envelopes, authentication, decoding, and rejection
before network access. Test MCP discovery/handlers when changing exposed APIs.
Leave generic parsing, logging, and packaging behavior to go-kit's module tests.
Keep local tests focused on Kea behavior, configuration discovery and precedence,
credential rules, secret-safe errors, and integration with the libraries.
Format Go files, run `go test ./...` and `go vet ./...`, and check diagnostics.
Scan vulnerabilities after module changes; distinguish existing findings.
Validate this skill using the available skill-creator `quick_validate.py`.
Report files, checks, and live/platform limits. Do not commit, install dependencies,
deploy, or broaden scope without authorization.
