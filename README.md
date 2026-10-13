# kea-mcp

Go STDIO Model Context Protocol server for the ISC Kea API. Supports direct
daemon HTTP/HTTPS endpoints and the older Kea Control Agent. All Kea access
goes through the API; no database connection is required.

Configuration uses a TOML dotfile loaded with go-kit `confx`.

## API modes

- `control-agent` (default): one endpoint routes requests to `agent`, `dhcp4`,
  `dhcp6`, or `d2`. Daemon requests include the Kea `service` array.
- `direct`: one endpoint belongs to the daemon selected by
  `api.service`. Requests omit `service`; other targets are rejected.
  Register separate MCP instances for multiple daemons.

Select the mode for your deployment. There is no automatic probing or fallback.
Direct daemon HTTP/HTTPS APIs arrived in Kea 3.0; older deployments can use the
Control Agent. See [ISC's API socket guidance](https://kb.isc.org/docs/kea-api-sockets).
Unix sockets and TLS client certificates are not currently implemented.

## Configuration

Configuration is selected in this order:

1. An explicit `--config PATH` (relative to the process working directory).
2. `.kea-mcp.toml` in the process working directory.
3. `~/.agents/.kea-mcp.toml`.
4. `~/.codex/.kea-mcp.toml`.

The first file supplies all settings; files and inherited environment settings
are not merged. A missing explicit file, unreadable selected file, or invalid
configuration stops startup. Discovery checks the working directory only, not
parent directories. If no file exists, startup stops with instructions to create
`.kea-mcp.toml` or use `--config`. No dotenv files are loaded.

Unknown tables and keys are rejected, including misspelled security settings.
HTTP response bodies are limited to 64 MiB by default. Set a positive integer
`max_response_bytes` under `[api]` to change the limit (maximum
9223372036854775806). Oversized replies fail explicitly; no partial response is
reported as success. Prefer paginated lease queries for large deployments.

Copy [.kea-mcp.example.toml](.kea-mcp.example.toml) to a discovered location, or
use a custom path. A direct DHCPv4 configuration looks like:

```toml
[api]
mode = "direct"
service = "dhcp4"
url = "https://kea.example:8443"
username = "YOUR_API_USERNAME"
password = "YOUR_API_PASSWORD"
```

For older deployments, use `mode = "control-agent"` (the default), omit
`service`, and point `url` at the Control Agent. Optionally set
`services = ["agent", "dhcp4"]` under `[api]` to restrict targets; otherwise
discovery probes `agent`, `dhcp4`, `dhcp6`, and `d2`. Missing daemons do not
prevent MCP startup. `services` must be non-empty, unique, and is only supported
in Control Agent mode. `username` and exactly one
of `password`, `password_env`, or `password_file` must be present.
`password_env = "KEA_API_PASSWORD"` reads a variable from the server process
environment. `password_file = "credentials/kea-password.txt"` reads a plain text
file; relative paths resolve from the TOML file's directory, and absolute paths
are supported. File contents use the same surrounding whitespace trimming as
other password sources, including trailing newlines. The file is read at startup;
restart the server after changing it. Missing or unreadable password files stop
startup. Both empty credentials are rejected by Basic Auth. Keep credential files
out of version control; the root TOML dotfile is ignored by Git.

### Agent configuration

Codex starts this server using `command` and `args`, just like other STDIO
MCP servers. Add this to your Codex `config.toml`:

```toml
[mcp_servers.kea]
default_tools_approval_mode = "prompt"
command = "/absolute/path/to/kea-mcp"
args = ["--config", "/absolute/path/to/.kea-mcp.toml"]
startup_timeout_sec = 90
```

Use `kea-mcp.exe` on Windows and TOML literal strings or forward slashes for
Windows paths. For discovery instead, omit `args` and set
`cwd = "/absolute/path/to/project"`. The agent configuration launches the
process; the separate `.kea-mcp.toml` supplies Kea settings. These options
are documented in the [Codex MCP documentation](https://developers.openai.com/codex/mcp/).

Clients using JSON configuration can use:

```json
{
  "mcpServers": {
    "kea": {
      "command": "/absolute/path/to/kea-mcp",
      "args": ["--config", "/absolute/path/to/.kea-mcp.toml"]
    }
  }
}
```

## Run and develop

Building from source requires Go 1.27.2 or newer. The minimum version includes
the standard library security fixes required by this server.

Install with Go:

```sh
go install github.com/rannday/kea-mcp@latest
```

Add `GOBIN` (or `GOPATH/bin` when `GOBIN` is unset) to your `PATH`, then use
`kea-mcp` as the command in your agent's MCP configuration. This installation
path requires a published version containing the root executable.

From a checkout:

```sh
go run . --config /absolute/path/to/.kea-mcp.toml
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

The vulnerability scan runs without installing a `govulncheck` executable.

For distribution, prefer standalone binaries for Windows, macOS, and Linux,
with amd64 and arm64 builds. Binary users do not need Go installed.
Release packaging uses the pinned [go-kit buildx](https://github.com/rannday/go-kit/tree/main/modules/buildx)
Go tool. To create archives and checksums for all six OS/CPU combinations:

```sh
go tool buildx -v 0.1.0 --name kea-mcp --main . --target windows/amd64 --target windows/arm64 --target linux/amd64 --target linux/arm64 --target darwin/amd64 --target darwin/arm64
```

Choose the release version explicitly. Output goes to `dist/<version>`.
This repository does not yet automate release publishing.

Runtime configuration uses [confx](https://github.com/rannday/go-kit/tree/main/modules/confx);
structured stderr logging uses [logx](https://github.com/rannday/go-kit/tree/main/modules/logx).
These modules and buildx are pinned in `go.mod`; no sibling checkout is needed.
HTTP request and response bodies are not logged.

## Tools and resources

- Command-specific tools use stable names: `status-get` becomes `kea_status_get`,
  `lease4-get` becomes `kea_lease4_get`, and `config-set` becomes `kea_config_set`.
  Each accepts `service` and optional JSON `arguments`; objects, arrays, and
  scalars are forwarded unchanged. Consult the deployed version's argument schema.
- `kea_discover`: refresh command availability and the tool inventory.
- `kea_raw_command`: disabled by default. Enable with `[tools] raw_command = true`
  for arbitrary commands, including custom hooks, subject to read-only policy.
- `kea://api/commands`: refresh discovery and report command-to-tool mappings.
- `kea://api/examples/status-get`: live
  responses and per-service errors. Direct mode queries only its selected daemon.

Read live configuration through `kea_config_get` so the client's tool approval
rules apply. Configuration is not exposed as an MCP resource.

Startup probes `list-commands` on configured services. Dedicated tools expose
only commands present in both the live inventory and the reviewed access policy;
read-only mode also hides write tools. Unknown live commands appear in discovery
when read-only mode is off, but require an opted-in raw tool to execute.
Commands depend on version, daemon, and loaded hooks; the
[latest API reference](https://kea.readthedocs.io/en/latest/api.html) may describe
commands your deployment lacks. Each command call rechecks availability on its
selected service before dispatch. This does not guarantee availability between
the probe and execution; Kea still validates the request. Mutations are not retried.

Run `kea_discover` after daemon or hook changes. Refresh adds/removes tools and
sends MCP tool-list change notifications; the agent client must support refreshing
its inventory. Failed services produce error entries alongside successful results.
If every service is unavailable, discovery remains callable for recovery.
Configuration outputs can contain secrets. HTTPS uses Go's normal certificate
validation. Tests currently use mocked APIs; live version compatibility is pending.

### Tool approvals

Configure approvals per stable tool name in the agent client. For example, add
these entries beneath the Codex server configuration shown above:

```toml
[mcp_servers.kea.tools]
kea_discover = { approval_mode = "approve" }
kea_status_get = { approval_mode = "approve" }
kea_list_commands = { approval_mode = "approve" }
kea_lease4_get = { approval_mode = "approve" }
kea_config_set = { approval_mode = "prompt" }
```

Keep the default approval mode as `prompt` for other tools. Approval of a command
tool covers that command on all configured services with any valid arguments.
It does not restrict individual targets or arguments. Use `api.services` and
separate MCP instances where different service policies are needed. Read tools
can reveal sensitive data; choose auto-approvals accordingly. Enabling and
approving `kea_raw_command` grants broad command access, so leave it disabled
when command-specific approval boundaries matter.

### Read-only mode

Set this independently of the transport mode in your selected TOML file:

```toml
[api]
read_only = true
# Include the endpoint and credentials as usual.
```

The default is `false`. When enabled, the shared Kea client permits only exact
command names classified as read in [ISC's API reference](https://kea.readthedocs.io/en/latest/api.html).
The reviewed policy in [kea/access.go](kea/access.go) lists all 219 documented
commands (89 read, 130 write), reviewed on 2026-10-13. Write commands and unknown
commands, including custom hooks and future additions, are rejected before any
HTTP request. This applies to raw commands, dedicated tools, and resources in
both Control Agent and direct modes. Restart after changing the setting.

`kea://api/commands` reports `read_only` and, when enabled, filters the live
command lists to allowed reads. Availability still depends on the deployed
version, service, and hooks. No command-name prefixes imply permission.
ISC labels `config-test`, `ha-heartbeat`, `interface-redetect`, and
`perfmon-get-all-durations` as write; these are blocked too.

This is a local MCP restriction; it does not change Kea credentials or permissions
for other clients. Read operations can still disclose configuration and credentials.
Agent tool approvals remain separate: approving `kea_raw_command` approves that
tool, while this server's read-only policy restricts the commands it can execute.
