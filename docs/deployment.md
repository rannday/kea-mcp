# Deployment and operations

## Obtain and verify a build

Release archives are available for Windows amd64, Linux amd64/arm64, and macOS
amd64/arm64. Select the archive matching your operating system and architecture.

Download the platform archive and `checksums.txt` from
[GitHub Releases](https://github.com/rannday/kea-mcp/releases). For example, the
Windows release archive is `kea-mcp-v0.1.0-windows-amd64.zip`.

For development snapshots, download `kea-mcp-snapshot-<commit>` from the successful
Actions run's artifacts. Unpack the outer download to obtain the platform
archives and `checksums.txt`. The SHA-256 entries cover the platform archives,
not the outer download. To verify a selected archive on Linux:

```sh
sha256sum kea-mcp-v0.1.0-linux-amd64.tar.gz
# Compare with checksums.txt before extracting or running the executable.
tar -xzf kea-mcp-v0.1.0-linux-amd64.tar.gz
./kea-mcp --help
```

Compare the hash with the matching entry in `checksums.txt`. Replace the example
version and platform with the archive you downloaded.
On Windows, compare the selected archive's `Get-FileHash -Algorithm SHA256`
result with its entry in `checksums.txt`, then extract the ZIP. Checksums detect
corruption; obtain them and the archives from the intended release or Actions run.
Retain the selected build and its checksum manifest in your deployment storage.
Each platform archive contains only the executable.
Keep configuration and credentials separately.

The MCP handshake reports the packaged version; source builds report `dev`.
Record the release or snapshot version, source commit, and checksum for support
and rollback.

## Configure and launch

1. Extract the executable into a directory dedicated to the selected build.
2. Copy the appropriate public TOML example into an operator-controlled location.
   Use explicit mode, services/endpoints, request timeout, and response limit.
3. Start the pilot with `[api] read_only = true` and leave
   `[tools] raw_command` disabled. Limit endpoints to the required daemons.
4. Use HTTPS for remote credentials. Configure trusted certificates and, where
   required, mutual TLS. Restrict access to configuration, password files, and
   client keys to the account launching the MCP process.
5. Configure the MCP client with an absolute executable path and
   an explicit `--config` path, or use home TOML discovery. For the Codex plugin,
   put the executable on the client's PATH and use home discovery. Set client
   timeouts to allow sequential discovery and availability checks. Keep per-tool
   approvals appropriate to the data or operations each tool exposes.

This is a STDIO process launched by the MCP client. It does not require a public
MCP listener or a separate background service. See the README for complete
configuration and client examples. stdout belongs exclusively to MCP; collect
stderr through the client for troubleshooting. Keep live configuration responses
and lease data out of shared logs and incident attachments.

## Acceptance after installation or upgrade

Initialize through the actual MCP client, run `kea_discover`, and inspect every
configured service's result. For each service, call `kea_status_get` and
`kea_version_get`: require no MCP `isError` and Kea result `0`. Record the daemon
version, loaded hooks, selected API mode, build commit, and checksum without
credentials or configuration bodies. Confirm command tools appear at startup,
discovery leaves the catalog unchanged, and the client can stop/restart the process cleanly.

For writable deployments, first test the intended commands and recovery procedure
in an isolated lab. Enabling writes requires a separate operator decision and
appropriate client approvals. API checks do not prove DHCP allocation, DNS
updates, or capacity under load. Repeat these checks against your deployed Kea
version and required hooks, and measure representative configuration/lease
responses, latency, and memory use.

## Upgrade, rotate, and recover

Retain the previous executable, approved TOML, and archive manifest. Stop the MCP
instance in its client, select the new executable path, restart, and repeat the
acceptance checks. To roll back, select the retained executable and compatible
configuration, restart, and check again. Binary rollback does not undo Kea state
changes made through tools.

Passwords and certificate files are loaded at startup. Rotate them through your
normal credential procedure, restart the MCP instance, and repeat discovery and
status/version checks. Coordinate trust changes with the Kea listener. No
certificate-verification bypass is available.

For unavailable services, check the mode, endpoint, listener, credentials, TLS,
and timeout without exposing secrets. Restore connectivity and run discovery
again; initialization alone does not verify Kea connectivity. Refresh discovery
after daemon or hook changes. A partial discovery result requires checking each
service independently.

If a write reports `outcome_unknown`, inspect the affected Kea state before
retrying. Transport failure does not prove the write failed. A Kea result `5`
means partial application and failed rollback; reconcile the running state with
the approved configuration. `config-set` changes running state; `config-write`
persists it. Recover using the deployment's approved Kea backup procedure.
