# Working on kea-mcp

## Scope

This is a Go STDIO MCP server for the ISC Kea API.

Read `.agents/skills/kea-mcp/SKILL.md` when changing Kea commands, transports,
configuration, MCP tools, resources, tests, or API documentation. That skill owns
the repository's Kea contracts and validation requirements.

## Layout

- `main.go`: configuration and STDIO lifecycle; root executable package.
- `config`: TOML discovery, validation, and credential resolution.
- `kea`: command envelopes, result handling, access policy, and HTTP transport.
- `server`: endpoint routing, MCP tool/resource registration, and handlers.
- `.kea-mcp.direct.example.toml` and `.kea-mcp.control-agent.example.toml`:
  explicit mode examples; direct uses separate DHCP4, DHCP6, and D2 endpoints.

## Documentation

README.md is for installing, configuring, and using the server. Keep both TOML
examples and README settings synchronized with configuration, including shared
and per-service authentication and client versus server timeouts. Keep
contributor and agent procedures in the project skill rather than the README.
The local `.kea-mcp.toml` may contain credentials; do not copy its contents into
documentation or fixtures.
