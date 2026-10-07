# Security

ePOS Proxy is designed to run on a trusted workstation and expose printing
endpoints to Odoo Point of Sale on the local machine or local network.

## Security controls

The proxy currently applies the following controls:

- Remote requests are rejected when **Allow Network Printing** is disabled.
- LAN printer IDs are accepted only when the decoded printer address exactly
  matches a printer configured in ePOS Proxy.
- Invalid explicit USB printer IDs are rejected instead of falling back to the
  first available USB printer.
- HTTP request bodies, headers and connection lifetimes are bounded.
- Server panics are recovered and logged instead of terminating the process.
- ESC/POS image dimensions and decoded image sizes are bounded.
- ePOS XML parsing is iterative, rejects nested command elements and limits the
  number of command elements per request.
- HTTPS private keys are stored with restrictive file permissions and the
  application uses TLS 1.2 or newer.
- Frontend dependencies are lockfile-based and installed with `npm ci`.
- Pull-request CI runs Go vulnerability analysis and npm security auditing.
- Dependency and GitHub Actions updates are monitored by Dependabot.

There is deliberately no application-level bearer token in printer URLs.
Network Printing should therefore only be enabled on networks where clients
that can reach the proxy are trusted. Browser Local Network Access permissions
provide an additional browser-side boundary, but they are not a replacement
for network trust.

## macOS legacy compatibility

The macOS artifacts intentionally preserve support for operating systems that
are no longer supported by current Go releases:

| Artifact | Minimum macOS | Go toolchain |
| --- | ---: | ---: |
| Apple Silicon | 11.0 | 1.24.13 |
| Intel | 10.15 | 1.22.12 |

These Go toolchains are end-of-life. Security fixes in newer Go standard
libraries cannot always be backported without raising the minimum supported
macOS version. For this reason CI performs a binary vulnerability scan for
these compatibility artifacts and exposes its findings as warnings rather than
failing the build solely because of vulnerabilities inherent to the legacy Go
runtime.

The normal source/dependency vulnerability scan is still blocking and runs
with the currently supported CI Go toolchain.

For installations that do not require Catalina or Big Sur compatibility, a
future build using a current Go toolchain should be preferred once such an
artifact is provided.

## Deployment guidance

Keep ePOS Proxy and the host operating system updated. Leave **Allow Network
Printing** disabled when Odoo runs on the same computer. When network printing
is required, restrict access to the workstation at the network/firewall layer
to the trusted POS network and configure only the printer addresses that are
actually required.

The HTTPS certificate authority created by ePOS Proxy is local to the
installation. Protect the application data directory and do not copy its
private CA key to other systems.
