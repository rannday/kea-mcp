# Real Kea integration tests

These tests start the lab in `integration/docker`, copied from `../kea-simple`:
ISC DHCP4, DHCP6, and DHCP-DDNS containers plus BIND 9.20. They launch an
extracted buildx executable over MCP STDIO, using a temporary explicit
configuration. There are no mock HTTP servers
or in-process MCP servers.

Docker must be running with Linux containers. Either `docker compose` or
`docker-compose` must be on PATH. Missing Docker, missing binaries, startup
failures, and unavailable daemons fail the tests; nothing silently skips.

## Run on Windows

From the repository root, build and extract the Windows archive:

```powershell
go tool buildx --version snapshot --name kea-mcp --main . --out dist --force --target windows/amd64
Expand-Archive -LiteralPath dist/kea-mcp-snapshot-windows-amd64.zip -DestinationPath dist/integration -Force
$env:KEA_MCP_BINARY = (Resolve-Path dist/integration/kea-mcp.exe).Path
go test -tags=integration -count=1 -timeout=10m -v ./integration
```

To test Kea 2.6.1 through the Control Agent with the same executable:

```powershell
$env:KEA_INTEGRATION_MODE = 'control-agent'
go test -tags=integration -count=1 -timeout=10m -v ./integration
Remove-Item Env:KEA_INTEGRATION_MODE
```

## Authentication and TLS profiles

Set `KEA_INTEGRATION_SECURITY` to `plain` (default), `basic`, `tls`, or `mtls`.
TLS and mutual TLS also require Basic Auth. Every profile runs the complete
suite against native Kea listeners; there is no TLS proxy.

For example, on Windows:

```powershell
$env:KEA_INTEGRATION_SECURITY = 'mtls'
go test -tags=integration -count=1 -timeout=10m -v ./integration
Remove-Item Env:KEA_INTEGRATION_SECURITY
```

Use either API mode with any profile. Pre-generated public test certificates and
private keys live in `testdata/certs`; see its README for validity and regeneration.
Each run copies them into a temporary directory alongside modified listener
configurations and removes those copies during cleanup. Credentials are public
lab values, with distinct users and passwords for each direct endpoint. No
production credentials are used.

## Run on Linux

```sh
go tool buildx --version snapshot --name kea-mcp --main . --out dist --force --target linux/amd64
mkdir -p dist/integration
tar -xzf dist/kea-mcp-snapshot-linux-amd64.tar.gz -C dist/integration
KEA_MCP_BINARY="$PWD/dist/integration/kea-mcp" go test -tags=integration -count=1 -timeout=10m -v ./integration
```

Set `KEA_INTEGRATION_MODE=control-agent` for the Kea 2.6.1 suite; the default is
`direct`, using latest Kea. On macOS, use `--target darwin/amd64` (Intel) or
`--target darwin/arm64` (Apple Silicon), extract the corresponding archive, and
run the same command with Docker available. With Colima, set `TMPDIR` to a
directory shared with its VM, such as `$HOME`; CI uses the runner's temporary
directory under its home.

CI runs native Go checks on Linux, Windows, and macOS. Real integration runs on
Linux and Intel macOS against both latest Kea directly and Kea 2.6.1 through the
Control Agent, with all four security profiles in each mode. macOS uses Colima
to run Linux containers. Each job tests its
native executable extracted from a buildx archive; artifact uploads require every
matrix job to pass. Windows hosted runners run native Go checks without this
Docker suite; the Windows integration commands above work with local Docker
Desktop in Linux container mode.

Ordinary `go test ./...` excludes this suite; the `integration` build tag and
`KEA_MCP_BINARY` opt in explicitly.

## What is verified

- Startup tool inventory, live command discovery, status and daemon versions for
  all three services.
- Complete `config-get` responses compared with independent HTTP responses
  from the same running daemons.
- Lease command hooks and genuine empty lease results on fresh volumes.
- The live command resource and read-only tool annotations.
- A raw configuration write rejected in read-only mode, with configuration hashes
  checked before and after. No write is permitted to execute.
- A real large configuration rejected by a small response-size limit, followed
  by a successful status request.
- DHCP6 stopped and restarted through Compose: partial discovery errors, working
  DHCP4/D2 calls, preserved tool inventory, and recovery with the same config.
- Native Basic Auth accepts valid credentials and rejects missing or wrong
  credentials. Direct endpoints reject another endpoint's credentials.
- Native TLS accepts the fixture's trusted server certificate and rejects an
  untrusted CA and a hostname mismatch.
- Native mutual TLS accepts a trusted client certificate and rejects missing
  client identities and identities signed by an unrelated CA.
- Authentication and TLS failures reach MCP calls and discovery; independent
  authenticated requests confirm the daemon remains healthy during each failure.
- Clean shutdown of the executable after the STDIO session closes.

## Fixture and isolation

The original entrypoints, D2 configuration, BIND configuration, and zone seeds
are copied from `../kea-simple`. DHCP4 and DHCP6 retain the original three
subnets and gain 64 subnets across 32 additional shared networks. Each family
has **67 subnets, 33 shared networks, and 2,049 reservations**, with multiple
pools, per-subnet options, and nested user metadata. The full live response is
compared, and fixture counts are asserted so an accidentally smaller config cannot
pass. Added IPv4 networks use the benchmarking range 198.18.0.0/15; IPv6 uses
2001:db8::/32.

Direct mode pulls upstream Kea `latest` images. Control Agent mode applies
`compose.control-agent.yaml`, pins all Kea services to `2.6.1`, and replaces
daemon HTTP listeners with UNIX control sockets shared with the agent. The agent
runs from ISC's DHCP4 image, which includes `kea-ctrl-agent`; no custom images are
built. Both modes use BIND `9.20`, a unique
Compose project, randomly selected host ports bound to 127.0.0.1 and fixed across restarts, and fresh named volumes.
The suite removes only that project's containers, network, and volumes, including
after test failures. It leaves the sibling lab alone. A force-killed test process
cannot run cleanup; the logged project name identifies any leftover resources.

No DHCP interfaces are enabled and no DHCP or DNS packet ports are published.
The copied TSIG key is a public lab fixture, never a production credential.
The additional subnets exercise API payloads; their zones are not added to BIND.
These tests do not claim DHCP allocation, DDNS update correctness, throughput,
production-scale capacity, or compatibility with the
cable plant's actual configuration. Those require separate tests.
