# TokenSmith authentication and authorization

Set `AUTH_PROVIDER=tokensmith` for TokenSmith authentication and Casbin
authorization. The default `jwks` provider disables authentication when no
JWKS URL is configured. If a URL is configured but keys cannot be loaded after
retries, PCS fails startup. Outbound `OAUTH2_*` settings for SMD work with either
provider.

Use these container environment variables, or export them before starting PCS:

```sh
AUTH_PROVIDER=tokensmith
AUTH_ISSUER=https://tokensmith.example
JWKS_URL=https://tokensmith.example/.well-known/jwks.json
AUTH_AUDIENCE=power-control
AUTHZ_MODE=enforce
AUTHZ_POLICY_PATH=/configs/authz/policy.csv
AUTHZ_GROUPING_PATH=/configs/authz/grouping.csv
```

The bundled policy grants `power-reader` read access and `power-operator` read,
write, and transition-delete access. Roles come from TokenSmith's `scope` array
without the `role:` prefix. Keycloak tokens require the `jwks` provider or
exchange through TokenSmith first.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--auth-provider` | `AUTH_PROVIDER` | `jwks` |
| `--jwks-url` | `JWKS_URL` | empty |
| `--auth-issuer` | `AUTH_ISSUER` | empty (required for TokenSmith) |
| `--auth-audience` | `AUTH_AUDIENCE` | `power-control` |
| `--authz-mode` | `AUTHZ_MODE` | `enforce` |
| `--authz-model-path` | `AUTHZ_MODEL_PATH` | TokenSmith's RBAC path model |
| `--authz-policy-path` | `AUTHZ_POLICY_PATH` | empty (required for enforce/shadow) |
| `--authz-grouping-path` | `AUTHZ_GROUPING_PATH` | empty |

Flags override their environment variables. `PCS_JWKS_URL` takes precedence
over both `JWKS_URL` and `--jwks-url`.

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
