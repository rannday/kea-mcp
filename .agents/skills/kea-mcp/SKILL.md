---
name: kea-mcp
description: Maintain this repository's Kea API contracts, HTTP transports, authentication, TOML configuration, MCP tools and resources, and related tests or documentation.
---

# Kea MCP development

## Repository contracts

Keep Kea access behind the API client; never add direct database access or require
database credentials. Support direct daemon HTTP/HTTPS APIs and the legacy
Control Agent. Prefer direct mode for Kea 3.0 and newer in usage guidance;
omitted api.mode still selects Control Agent for compatibility. Keep the direct
and Control Agent example TOML files explicit about mode and authentication.

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
reference reads credentials from the environment, including endpoint references.
Omitted credentials mean no auth; explicit auth=none rejects credential fields.
Basic auth (explicit or inferred from credential fields) requires username and
exactly one of password, password_env, or password_file. Endpoint tables inherit
shared [api] authentication only when all local auth/credential fields are absent;
otherwise require a complete local set or auth=none. Resolve relative
password_file paths from the TOML directory. Preserve credential presence checks and surrounding
whitespace trimming; never include credential contents in errors.

Explicit --config accepts any filename containing valid Kea TOML, including
extensionless files. Automatic discovery accepts only .kea-mcp.toml.
Shared api.tls supports ca_file and paired cert_file/key_file. Endpoint TLS
tables replace the whole shared table; an empty table clears custom TLS settings.
TLS inheritance is independent of authentication. Resolve certificate paths
relative to the selected TOML directory or accept absolute paths. Load files at
startup with secret-safe errors. Add custom CAs to system roots, preserve
hostname and expiration checks, and reject TLS settings on HTTP. Never add a
verification bypass. Keep endpoint TLS transports isolated and preserve request
timeouts, redirect rejection, and caller-owned clients. Test with synthetic
certificates (public pre-generated integration fixtures or generated unit-test
identities), including mTLS and failed handshakes; do not inspect real
certificate or private-key files.

api.read_timeout_sec defaults to five seconds and bounds the entire HTTP request.
--read-timeout-sec overrides the TOML value. api.max_response_bytes defaults to
64 MiB; reject oversized replies without reporting partial data as success.
MCP startup_timeout_sec and tool_timeout_sec belong to the client configuration,
not the server TOML. Initialization makes no Kea requests; discovery and command
availability checks can require multiple sequential requests.

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
and rejects other targets. Support one api.url with api.service or multiple
api.endpoints.dhcp4/dhcp6/d2 tables. Multiple endpoints require direct mode and
reject api.url, api.service, and api.services. Route exactly one service per
request; never send credentials or requests to another daemon's endpoint.
Never probe using mutations or silently fall back after failures.
HA listeners are not general API endpoints. Preserve context cancellation and
bounded timeouts. Endpoint URLs require an HTTP/HTTPS scheme and accept DNS names,
IPv4, and bracketed IPv6 addresses. Reject redirects; validate HTTPS certificates.

HTTP responses use an array envelope, including direct daemon responses.
Check HTTP status and Kea results: 0 success; 1 failure; 2 unsupported;
3 empty/no affected entities; 4 conflict; 5 failed rollback after partial apply.
Interpret results by command and deployed version. HTTP 200 alone is not success.
Preserve every valid response envelope on failure. A dispatched mutation without
a definitive reply must report outcome_unknown; never retry it automatically.

Arguments vary by command. Preserve object, array, and scalar JSON arguments;
Kea validates the deployed command's schema. Dedicated tools bind a single
command and use stable kea_ names with hyphens replaced by underscores. Preserve
existing tool names and resource URIs unless an interface change is requested.
Register all reviewed command tools at startup without contacting Kea; hide write
tools in read-only mode. Keep the catalog fixed for the process lifetime. Discovery
reports live per-service availability and matching tools, without adding/removing
tools or requiring clients to refresh their catalog.
Raw access requires [tools] raw_command=true and cannot bypass read-only policy.
Recheck availability on the selected service before execution. Refresh discovery
through kea_discover or the commands resource. Preserve successful
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

For an authorized live connection check, use the operator's selected configuration
and confirm its mode rather than inferring it from an example filename. Avoid
displaying credential settings. Initialize MCP, run kea_discover, and check
kea_status_get and kea_version_get for each configured service. Inspect MCP errors,
per-service discovery errors, and Kea result codes. These read-only checks prove
API access, not DHCP/DNS behavior, other authentication modes, or mutation safety.
