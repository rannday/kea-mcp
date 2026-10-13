# Working on kea-mcp

## Scope

This is a Go STDIO MCP server for the ISC Kea API.

Read `.agents/skills/kea-mcp/SKILL.md` when changing Kea commands, transports,
configuration, MCP tools, resources, tests, or API documentation. That skill owns
the repository's Kea contracts and validation requirements.

## Layout

- `main.go`: configuration and STDIO lifecycle; root executable package.
- `config`: TOML file discovery and validation.
- `kea`: command envelopes, result handling, authentication, HTTP transport.
- `server`: MCP tool/resource registration and handlers.

## Documentation

README.md is for installing, configuring, and using the server. Keep its examples
synchronized with configuration. Keep contributor and agent procedures in the
project skill rather than the README.
