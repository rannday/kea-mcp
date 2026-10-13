---
name: kea-mcp
description: Maintain this repository's Kea API contracts, HTTP transports, authentication, TOML configuration, MCP tools and resources, and related tests or documentation.
---

# Kea MCP development

## Repository contracts

Keep Kea access behind the API client; never add direct database access or require
database credentials. Support direct daemon HTTP/HTTPS APIs and the legacy
Control Agent, which remains the default mode.

Keep MCP protocol output on stdout and use go-kit `logx` for stderr logs.
Do not enable HTTP body logging. Configuration responses can contain credentials;
use synthetic fixtures and redact live evidence before storing or sharing it.

Use go-kit `confx` for TOML decoding and the pinned `go tool buildx` for release
archives.

`logx`, `buildx`, and `confx` own their functionality and module tests in go-kit.
Use their public APIs; never extend, reimplement, or alter module functionality
in this repository. Fix module issues and make module changes or improvements
in the go-kit repository, with the corresponding tests there. Do not add local
replacements or workarounds for behavior those modules should own, or duplicate
tests they already own. Test only this repository's integration with the modules
and its Kea-specific policy.

Configuration selects --config, then .kea-mcp.toml in cwd, ~/.agents/, and
~/.codex/ in order. Require a file; never merge sources or fall back after a
selected file fails. Do not load dotenv files. Only an explicit api.password_env
reference reads credentials from the environment. Require exactly one of
password, password_env, or password_file. Resolve relative password_file paths
from the TOML directory. Preserve credential presence checks and surrounding
whitespace trimming; never include credential contents in errors.

## Confirm the API contract

Use the deployed version's official sources through the global documentation
lookup workflow:

- [API sockets and migration](https://kb.isc.org/docs/kea-api-sockets)
- [API reference](https://kea.readthedocs.io/en/latest/api.html)
- [Control channel protocol](https://kea.readthedocs.io/en/latest/arm/ctrl-channel.html)

Establish the deployed Kea version, target daemon, transport mode, and hooks.
The latest manual can include development commands.
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
HA listeners are not general API endpoints. Preserve context cancellation and
bounded timeouts.

HTTP responses use an array envelope, including direct daemon responses.
Check HTTP status and Kea results: 0 success; 1 failure; 2 unsupported;
3 empty/no affected entities; 4 conflict; 5 failed rollback after partial apply.
Interpret results by command and deployed version. HTTP 200 alone is not success.

Arguments vary by command. Preserve object, array, and scalar JSON arguments;
Kea validates the deployed command's schema. Dedicated tools bind a single
command and use stable kea_ names with hyphens replaced by underscores. Preserve
existing tool names and resource URIs unless an interface change is requested.
Expose only reviewed commands discovered live; hide write tools in read-only mode.
Raw access requires [tools] raw_command=true and cannot bypass read-only policy.
Recheck availability on the selected service before execution. Refresh discovery
and tools through kea_discover or the commands resource. Preserve successful
results alongside per-service errors, and honor optional Control Agent api.services.

## Handle operational intent

Raw commands include configuration changes, lease mutations, and shutdown;
describe their capabilities accurately. Live Kea mutations require explicit
operator authorization. `config-test` does not guarantee
`config-set` success; `config-set` changes running state, while `config-write`
persists it. Avoid retries without establishing a mutation's outcome.

## Implement and verify

Mock HTTP endpoints to inspect envelopes, authentication, decoding, and rejection
before network access. Cover both modes and invalid targets. Test MCP
discovery/handlers when changing exposed APIs.
Keep local tests focused on Kea behavior, configuration discovery and precedence,
credential rules, secret-safe errors, and integration with the libraries.
For Go changes, run `go test ./...` and `go vet ./...` in addition to the global
Go workflow. State whether Kea validation used mocks or a live deployment.
