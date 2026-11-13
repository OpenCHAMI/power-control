# Security Review — 2026-11-13

Scope: staged changes pending commit (JAWS PDU support, Dockerfiles, HSM state manager, TRS
local client), with specific attention to hardcoded passwords, secrets, and credential exposure.

## Summary

| # | Severity | File | Lines | Vulnerability | Confidence |
|---|----------|------|-------|---------------|------------|
| 1 | 🟡 MEDIUM | internal/domain/jaws.go (+ Dockerfile, Dockerfile.ct.Dockerfile, Dockerfile.pprof, Dockerfile.test.unit.Dockerfile) | 99–113 | TLS certificate verification for JAWS PDU connections disabled by default | 8/10 |

No hardcoded passwords, API keys, tokens, private keys, or other secrets were found in any
staged file.

## Finding 1 — JAWS TLS verification disabled by default (MEDIUM, 8/10)

**Category:** Authentication failure / security misconfiguration

**Problem:** `jawsInsecureSkipVerify()` in `internal/domain/jaws.go` initializes
`skipVerify := true` and only enables certificate verification if
`PCS_JAWS_INSECURE_SKIP_VERIFY` is explicitly set to a parseable `false`. In addition, the
production `Dockerfile` (not just the test images) sets
`ENV PCS_JAWS_INSECURE_SKIP_VERIFY="true"`, so verification is off by default twice over.

`JawsMonitor` → `JawsLoad` sends Vault-stored PDU controller credentials
(`GLOB.CS.GetControllerCredentials`) as HTTP Basic Auth over this unverified TLS connection
every poll interval (default 20s). An attacker able to perform a MITM on the management
network (ARP spoofing, rogue DHCP/DNS, compromised switch) could present any certificate,
harvest the PDU admin credentials, and subsequently control power to cabinets or feed false
power-state data back into PCS.

This inverts the existing codebase pattern (`internal/taskrun/trshttp_local.go`,
`cmd/power-control/http_clients.go`), where the CA-verified client (`PCS_CA_URI`) is the
normal path and insecure is a fallback. Notably, `jawsTLS()` already implements full CA
verification (honoring `PCS_CA_URI` plus system CAs) — it is simply defaulted off.

**Suggested fix:**
1. Default `skipVerify` to `false` (verify certificates when `PCS_JAWS_INSECURE_SKIP_VERIFY`
   is unset or invalid).
2. Remove `ENV PCS_JAWS_INSECURE_SKIP_VERIFY="true"` from the production `Dockerfile` so
   operators must explicitly opt in to insecure mode for PDUs with self-signed certificates.

## Items reviewed and not reported as findings

- **internal/hsm/state_manager.go** (user-tagged): no secrets. `ReservationKey`/`DeputyKey`
  are HSM reservation handles, not credentials. The staged change (JSON re-marshal fallback
  for `RedfishOutletInfo` plus a nil-check) has no security impact.
- **Legacy RSA cipher suites** (`jawsSuites()`, `trsCipherSuites()`): weak (no forward
  secrecy, CBC-SHA), but documented as required for older ServerTech JAWS PDU firmware and
  appended alongside modern ECDHE suites. Accepted for backward compatibility.
- **docker-compose.test.ct.yaml** `smd:2.20` → `smd:latest`: mutable tag, but an unstaged
  change in CT-test-only tooling.
- **`generateTransitionPayload`** `fmt.Sprintf` of `resetType`: value comes from PCS's
  internal allowable-actions enumeration, not user input — no injection path.
- **Credential handling in `JawsMonitor`**: only the Vault lookup error is logged, never
  the password.
