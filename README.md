# kea-mcp

Go STDIO Model Context Protocol server for the ISC Kea API. Supports direct
daemon HTTP/HTTPS endpoints and the older Kea Control Agent. All Kea access
goes through the API; no database connection is required.

Configuration uses a TOML dotfile.

## Installation

Install with Go 1.27.2 or newer:

```sh
go install github.com/rannday/kea-mcp@latest
```

Add `GOBIN` (or `GOPATH/bin` when `GOBIN` is unset) to your `PATH`, then use
`kea-mcp` as the command in your MCP client configuration. This installation
path requires a published version containing the root executable.

To run from a source checkout:

```sh
go run . --config /absolute/path/to/.kea-mcp.toml
```

## API modes

- `direct`: use `api.url` with `api.service` for one daemon, or
  `api.endpoints` for multiple daemons in one MCP instance. Requests omit
  the wire `service` field and go only to the selected daemon's endpoint.
- `control-agent`: one endpoint routes requests to `agent`, `dhcp4`,
  `dhcp6`, or `d2`. Daemon requests include the Kea `service` array.

Use direct mode for Kea 3.0 and newer; older deployments can use the Control
Agent. Both example files select their mode explicitly. Omitting `api.mode`
still selects `control-agent` for compatibility. There is no automatic probing
or fallback. See [ISC's API socket guidance](https://kb.isc.org/docs/kea-api-sockets).
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

Kea HTTP requests have a five-second timeout by default. Set `[api]`
`read_timeout_sec` to a positive integer in seconds (maximum 9223372036),
or pass `--read-timeout-sec N` to override the TOML value. Despite its name,
this bounds the entire HTTP request, including connecting and reading the body.
Increase it for commands that take longer, such as large configuration updates.

Copy [.kea-mcp.direct.example.toml](.kea-mcp.direct.example.toml) or
[.kea-mcp.control-agent.example.toml](.kea-mcp.control-agent.example.toml) to
`.kea-mcp.toml` in a discovered location, or use a custom path with `--config`.
The direct example connects to DHCP4, DHCP6, and D2 on `127.0.0.1` ports 8000,
8001, and 8002; the Control Agent example uses port 8000. Adjust these to match
your listeners; these are example ports, not ports configured by this server.

`url` accepts DNS names or IP addresses with an `http://` or `https://` scheme,
such as `http://127.0.0.1:8000` or `http://[::1]:8000`. A bare IP address is not
a complete URL. A direct DHCPv4 configuration looks like:

```toml
[api]
mode = "direct"
service = "dhcp4"
read_timeout_sec = 90
url = "https://kea.example:8443"
username = "YOUR_API_USERNAME"
password = "YOUR_API_PASSWORD"
```

For older deployments, use `mode = "control-agent"` (the default), omit
`service`, and point `url` at the Control Agent. Optionally set
`services = ["agent", "dhcp4"]` under `[api]` to restrict targets; otherwise
discovery probes `agent`, `dhcp4`, `dhcp6`, and `d2`. Missing daemons do not
prevent MCP startup. `services` must be non-empty, unique, and is only supported
in Control Agent mode.

Authentication is optional in both modes. Omit all credential settings, or set
`auth = "none"`, to send no Authorization header. `auth = "none"` rejects
credential settings rather than silently ignoring them. Supplying credentials
implicitly selects Basic auth; `auth = "basic"` can make that explicit.
Basic auth requires `username` and exactly one
of `password`, `password_env`, or `password_file`.
`password_env = "KEA_API_PASSWORD"` reads a variable from the server process
environment. `password_file = "credentials/kea-password.txt"` reads a plain text
file; relative paths resolve from the TOML file's directory, and absolute paths
are supported. File contents use the same surrounding whitespace trimming as
other password sources, including trailing newlines. The file is read at startup;
restart the server after changing it. Missing or unreadable password files stop
startup. A credential pair with both values empty is rejected. Keep credential
files out of version control; the root TOML dotfile is ignored by Git.

### Multiple daemon endpoints

For direct APIs, configure any combination of `dhcp4`, `dhcp6`, and `d2`:

```toml
[api]
mode = "direct"
read_timeout_sec = 90
# Optional shared credentials for endpoints without their own auth settings.
username = "SHARED_API_USERNAME"
password_env = "KEA_SHARED_PASSWORD"

[api.endpoints.dhcp4]
url = "https://kea.example:8444"
# Inherits shared credentials.

[api.endpoints.dhcp6]
url = "https://kea.example:8446"
username = "DHCP6_API_USERNAME"
password_env = "KEA_DHCP6_PASSWORD"

[api.endpoints.d2]
url = "http://kea.example:8002"
auth = "none"
```

Endpoint tables accept `url`, `auth`, `username`, `password`, `password_env`,
and `password_file`. An endpoint with no auth or credential settings inherits
the `[api]` authentication settings. To override them, supply a complete local
credential set or `auth = "none"`; partial credential overrides are rejected.
With no shared or local credentials, endpoints use no authentication.
Password-file paths resolve from the TOML directory for every endpoint.
Only configured services are exposed. `api.endpoints` requires direct mode and
cannot be combined with `api.url`, `api.service`, or `api.services`.
Read-only policy, response limits, and request timeouts apply to every endpoint.
Restart the MCP server after changing endpoint or authentication settings.

### MCP client configuration

Codex starts this server using `command` and `args`, just like other STDIO
MCP servers. Add this to your Codex `config.toml`:

```toml
[mcp_servers.kea]
default_tools_approval_mode = "prompt"
command = "/absolute/path/to/kea-mcp"
args = ["--config", "/absolute/path/to/.kea-mcp.toml", "--read-timeout-sec", "90"]
startup_timeout_sec = 90
tool_timeout_sec = 420
```

Use `kea-mcp.exe` on Windows and TOML literal strings or forward slashes for
Windows paths. For discovery instead, omit `args` and set
`cwd = "/absolute/path/to/project"`. The agent configuration launches the
process; the separate `.kea-mcp.toml` supplies Kea settings. These options
are documented in the [Codex MCP documentation](https://developers.openai.com/codex/mcp/).

`startup_timeout_sec` and `tool_timeout_sec` are Codex client settings, not
Kea server TOML settings. Pass the server's timeout through `args` as shown;
adding a custom `read_timeout_sec` key beneath `[mcp_servers.kea]` does not
configure this process. Allow time for multiple requests per tool: discovery
can query four services sequentially, and commands probe availability before
execution. MCP initialization itself sends no Kea requests.

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
  responses and per-service errors. Direct mode queries only configured daemons.

Read live configuration through `kea_config_get` so the client's tool approval
rules apply. Configuration is not exposed as an MCP resource.

Run `kea_discover` first (or read `kea://api/commands`) to probe `list-commands`
and register dedicated tools. Startup exposes discovery and the optional raw
tool immediately, even when Kea is unavailable. Dedicated tools expose
only commands present in both the live inventory and the reviewed access policy;
read-only mode also hides write tools. Unknown live commands appear in discovery
when read-only mode is off, but require an opted-in raw tool to execute.
Commands depend on version, daemon, and loaded hooks; the
[latest API reference](https://kea.readthedocs.io/en/latest/api.html) may describe
commands your deployment lacks. Each command call rechecks availability on its
selected service before dispatch. This does not guarantee availability between
the probe and execution; Kea still validates the request. Mutations are not retried.

Dispatched command failures return MCP `isError` with structured `service`,
`command`, `error`, and all valid Kea `responses`, including result codes, text, and
arguments. Mixed success/failure replies preserve every envelope. Result 5
explicitly reports partial configuration application and failed rollback;
the previous configuration was not restored. A dispatched write without a
definitive reply (timeout, connection failure, malformed or oversized response)
reports `outcome_unknown = true`. Reconcile Kea state before retrying it.

Run `kea_discover` after daemon or hook changes. Refresh adds/removes tools and
sends MCP tool-list change notifications; the agent client must support refreshing
its inventory. Failed services produce error entries alongside successful results.
If every service is unavailable, discovery remains callable for recovery.
Configuration outputs can contain secrets; HTTP request and response bodies are
not logged. HTTPS validates server certificates. HTTP redirects are rejected.

### Checking the connection

Run `kea_discover` and check each configured service's discovery result for
errors. Then call `kea_status_get` and `kea_version_get` with the same service,
for example:

```json
{"service": "dhcp4"}
```

Repeat for `dhcp6` and `d2` when configured. These are read-only API checks;
successful status and version calls have no MCP `isError` and Kea result code
`0`. Check per-service errors even when discovery succeeds for other services.
MCP initialization alone does not test connectivity to Kea.

For connection failures, check the selected TOML file, explicit mode, listener
address and port, and authentication settings. A direct deployment must point
each service at its own API listener. This checks API access; it does not test
DHCP lease allocation or DNS updates.

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
It does not restrict individual targets or arguments. Limit targets through
`api.services` in Control Agent mode or the configured endpoints in direct mode.
Use separate MCP instances where different service policies are needed. Read tools
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
Write commands and unknown commands, including custom hooks and future
additions, are rejected before any HTTP request. This applies to raw commands,
dedicated tools, and resources in
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
