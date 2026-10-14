# Deployment and operations

## Validation status

The operator has tested direct API access against the latest DHCP4, DHCP6, and
D2 images in the sibling `kea-simple` Docker setup. Record the exact image
digests and `version-get` results with each deployment: `latest` is a moving
label. Automated repository tests exercise synthetic endpoints in both direct
and Control Agent modes, authentication, TLS, access policy, and failure handling.

Large configuration and lease-volume testing is deferred. The planned cable
plant validation target runs Kea 2.6.1. Treat that version and its installed hooks
as a separate acceptance target; use Control Agent mode for that deployment.
The current lab evidence does not establish its compatibility or capacity.

## Obtain and verify a build

CI builds deployment archives only after the Windows/Linux checks and
vulnerability check pass. It uses the `buildx` module pinned in `go.mod` through
`go tool buildx`. Targets are Windows amd64, Linux amd64/arm64, and macOS
amd64/arm64. Cross-compilation does not establish runtime compatibility on every
target; CI smoke-tests the packaged Linux amd64 executable.

Download `kea-mcp-snapshot-<commit>` from the successful Actions run's artifacts.
Confirm the source commit and check results before selecting an artifact,
especially for pull requests. Unpack the outer artifact download to obtain the
platform archives and `checksums.txt`. The SHA-256 entries cover the platform
archives, not the outer download. On Linux:

```sh
sha256sum --check checksums.txt
tar -xzf kea-mcp-snapshot-<commit>-linux-amd64.tar.gz
./kea-mcp --help
```

On Windows, compare the selected archive's `Get-FileHash -Algorithm SHA256`
result with its entry in `checksums.txt`, then extract the ZIP. Checksums detect
corruption; obtain them and the archives from the intended Actions run.
Artifacts expire after 30 days; retain an approved build and its manifest in
your deployment storage. Each platform archive contains only the executable.
Keep configuration and credentials separately.

To reproduce an archive set from a clean checkout with the required Go toolchain:

```sh
go tool buildx -v snapshot-<commit> --name kea-mcp --main . --out dist
```

Replace `<commit>` with the checked-out full commit hash. The output directory
must be empty. No tags or GitHub releases are created by this pipeline. Snapshot
names identify builds; the MCP implementation version currently remains `0.1.0`,
so record the artifact commit and checksum for support and rollback.

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
   `--config /absolute/path/to/kea-production.toml`. Set client timeouts to allow
   the server's sequential discovery and availability checks. Keep per-tool
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
updates, or cable-plant capacity. Before deploying to Kea 2.6.1, repeat these
checks against that version and the required hooks and measure representative
configuration/lease responses, latency, and memory use.

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

CI also runs weekly to detect newly disclosed vulnerabilities in unchanged
dependencies. Review security failures and dependency update PRs before approving
another deployment.
