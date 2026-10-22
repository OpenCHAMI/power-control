# Power Control Service (pcs)

This is a fork of the original PCS code from [Cray-HPE/hms-power-control](https://github.com/Cray-HPE/hms-power-control). Notable updates include:

- Public release infrastructure.
- Testing pipeline decoupled from private infrastructure.
- Postgres storage provider.
- CLI configuration through environment variables and multiple storage providers.
- Direct push-based state syncing without Kafka.
- Public multi-architecture container builds.
- Optional JWT/JWKS API authentication.
- Optional [TokenSmith authentication and authorization](docs/tokensmith.md).
- OAuth2 access tokens for SMD calls.
- Fake Vault credential store support for local and test deployments.

## CA trust

Set `PCS_CA_URI` to a CA bundle file or the hms-certs Vault URI,
`vault://pki_common/ca_chain`.
PCS loads and validates the bundle at startup, before starting workers. An
unreadable or invalid bundle prevents startup. SMD requests, including
reservations, verify TLS against the system roots and this bundle, without
insecure fallback. Redfish falls back to a connection without certificate
verification if the verified connection fails.

Without `PCS_CA_URI`, the existing unverified TLS behavior is preserved.

CA bundle changes require a PCS restart. For rotation, include both the current
and replacement CAs in the bundle and restart PCS before replacing server
certificates.

## Contributing

Commit messages should follow [Conventional Commits](https://www.conventionalcommits.org/).
