# TokenSmith authentication and authorization

Set `AUTH_PROVIDER=tokensmith` for TokenSmith authentication and Casbin
authorization. The default `jwks` provider disables authentication when no
JWKS URL is configured. If a URL is configured but keys cannot be loaded after
retries, PCS fails startup. Outbound `OAUTH2_*` settings for SMD work with either
provider.

Use these container environment variables, or export them before starting PCS:

```sh
AUTH_PROVIDER=tokensmith
TOKENSMITH_ISSUER=https://tokensmith.example
TOKENSMITH_JWKS_URL=https://tokensmith.example/.well-known/jwks.json
TOKENSMITH_AUDIENCE=power-control
TOKENSMITH_AUTHZ_MODE=enforce
TOKENSMITH_AUTHZ_POLICY_PATH=/configs/authz/policy.csv
TOKENSMITH_AUTHZ_GROUPING_PATH=/configs/authz/grouping.csv
```

The bundled policy grants `power-reader` read access and `power-operator` read,
write, and transition-delete access. Roles come from TokenSmith's `scope` array
without the `role:` prefix. Keycloak tokens require the `jwks` provider or
exchange through TokenSmith first.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--auth-provider` | `AUTH_PROVIDER` | `jwks` |
| `--tokensmith-jwks-url` | `TOKENSMITH_JWKS_URL` | empty (required) |
| `--tokensmith-issuer` | `TOKENSMITH_ISSUER` | empty (required for TokenSmith) |
| `--tokensmith-audience` | `TOKENSMITH_AUDIENCE` | `power-control` |
| `--tokensmith-authz-mode` | `TOKENSMITH_AUTHZ_MODE` | `enforce` |
| `--tokensmith-authz-model-path` | `TOKENSMITH_AUTHZ_MODEL_PATH` | TokenSmith's RBAC path model |
| `--tokensmith-authz-policy-path` | `TOKENSMITH_AUTHZ_POLICY_PATH` | empty (required for enforce/shadow) |
| `--tokensmith-authz-grouping-path` | `TOKENSMITH_AUTHZ_GROUPING_PATH` | empty |

Flags override their environment variables. The `jwks` provider keeps its
existing `JWKS_URL` / `--jwks-url` setting, with `PCS_JWKS_URL` taking precedence
over both. These settings do not affect the `tokensmith` provider.

All TokenSmith modes require authentication:

- `enforce` returns `403` for policy denials.
- `shadow` evaluates policy but allows authenticated requests.
- `off` skips authorization.

Missing or invalid tokens return `401` on protected routes. GET `/liveness`,
`/readiness`, `/health`, and their `/v1` aliases are public. Profiling routes under
`/v1/debug/pprof` are also public when built with the `pprof` tag.
TokenSmith validates token claims,
signature, issuer, audience, and expiration. Invalid configuration, unreadable
policy files, or unusable JWKS prevent startup.

Policies match normalized paths, including `/v1`, with `read` for GET/HEAD,
`write` for POST/PUT/PATCH, and `delete` for DELETE. Custom models accept
`sub, obj, act`. Policy and grouping paths accept files or directories of CSV
fragments.

Restart PCS to reload policy changes. Authorization decisions are logged in
`enforce` and `shadow` modes using PCS's `LOG_LEVEL` setting. Allows use debug
level and denials use info, including denials allowed through in `shadow` mode.
Use `shadow` with `LOG_LEVEL=INFO` to review a policy before enforcing it.
Logs and denial responses include `policy_version`.

## Outbound authentication to SMD

`SMD_AUTH_PROVIDER` selects authentication for requests PCS sends to SMD,
independently of incoming authentication. The default `oauth2` retains the
existing `OAUTH2_*` client credentials settings and sends no token when they
are unset. Selecting `tokensmith` ignores those settings.

```sh
SMD_AUTH_PROVIDER=tokensmith
SMD_TOKENSMITH_URL=https://tokensmith.example
TOKENSMITH_BOOTSTRAP_TOKEN=<bootstrap-token>
```

Provision a bootstrap token with the audience and scopes required by SMD.
SMD must trust TokenSmith's signing keys. PCS exchanges the bootstrap token
at startup, caches the service token, and refreshes it as needed for outbound
requests. Concurrent requests share the token and serialize refreshes. PCS
does not forward the incoming caller's token.

Bootstrap tokens are single use. A crash, node drain, or rolling update requires
a new token. Reusing the old token causes startup to fail on every restart. Use
mTLS service identity for unattended production restarts, or automate provisioning
a fresh bootstrap token for each startup. For mTLS, set
`TOKENSMITH_SERVICE_IDENTITY_CERT` and `TOKENSMITH_SERVICE_IDENTITY_KEY` to
mounted PEM files.
The certificate pair takes precedence over a bootstrap token and requires HTTPS.
These credential variables are read directly by the TokenSmith client and have
no CLI flags.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--smd-auth-provider` | `SMD_AUTH_PROVIDER` | `oauth2` |
| `--smd-tokensmith-url` | `SMD_TOKENSMITH_URL` | empty |
| `--smd-tokensmith-ca-file` | `SMD_TOKENSMITH_CA_FILE` | empty (system trust) |

The optional CA file adds trusted certificates for the TokenSmith connection.
SMD's existing TLS settings are unchanged. Invalid credentials or a failed
initial exchange prevent startup. Refresh failures fail the outbound request
without falling back to unauthenticated access. When the refresh session expires,
mTLS clients create a new session using the current certificate files.
Bootstrap-only clients require a restart with a new bootstrap token. Once token
acquisition fails, readiness returns `503` and health reports SMD as unresponsive.
Liveness remains healthy, so it does not trigger a Kubernetes restart.

Bootstrap exchange makes one attempt because a lost response may mean the token
was already consumed. mTLS session establishment retains retries. Use HTTPS for
TokenSmith connections. PCS logs a warning for plaintext HTTP and for settings
ignored by the selected outbound provider.
