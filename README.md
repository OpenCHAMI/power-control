# Power Control Service (pcs)

This is a fork of the original PCS code from [Cray-HPE/hms-power-control](https://github.com/Cray-HPE/hms-power-control). Notable updates include:

- Public release infrastructure.
- Testing pipeline decoupled from private infrastructure.
- Postgres storage provider.
- CLI configuration through environment variables and multiple storage providers.
- Direct push-based state syncing without Kafka.
- Public multi-architecture container builds.
- Optional JWT/JWKS API authentication.
- OAuth2 access tokens for SMD calls.
- Fake Vault credential store support for local and test deployments.

## Contributing

Commit messages should follow [Conventional Commits](https://www.conventionalcommits.org/).
