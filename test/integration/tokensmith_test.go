//go:build integration_tests

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

const tokenSmithIssuer = "http://tokensmith:8080"

func (s *IntegrationTestSuite) startTokenSmith() {
	s.tokensmith = newContainer(s.T(), testcontainers.ContainerRequest{
		Image: "ghcr.io/openchami/tokensmith:v0.4.2",
		Env: map[string]string{
			"TOKENSMITH_ISSUER":                  tokenSmithIssuer,
			"TOKENSMITH_OIDC_PROVIDER":           "http://keycloak:8080/realms/pcs-auth-test",
			"TOKENSMITH_CONFIG":                  "",
			"TOKENSMITH_KEY_DIR":                 "/tmp/keys",
			"TOKENSMITH_RFC8693_BOOTSTRAP_STORE": "/tmp/bootstrap",
			"TOKENSMITH_RFC8693_REFRESH_STORE":   "/tmp/refresh",
		},
		Networks:       []string{s.network},
		NetworkAliases: map[string][]string{s.network: {"tokensmith"}},
		ExposedPorts:   []string{"8080/tcp"},
		WaitingFor:     wait.ForHTTP("/.well-known/jwks.json").WithPort("8080/tcp"),
	}, true)
}

func tokenSmithEnv() map[string]string {
	return map[string]string{
		"AUTH_PROVIDER":       "tokensmith",
		"AUTH_ISSUER":         tokenSmithIssuer,
		"AUTH_AUDIENCE":       "power-control",
		"PCS_JWKS_URL":        tokenSmithIssuer + "/.well-known/jwks.json",
		"AUTHZ_POLICY_PATH":   "/configs/authz/policy.csv",
		"AUTHZ_GROUPING_PATH": "/configs/authz/grouping.csv",
	}
}

func (s *IntegrationTestSuite) mintTokenSmithToken(scope string) string {
	t := s.T()
	t.Helper()
	// Use TokenSmith's CLI and the running issuer's key to create test callers.
	cmd := []string{
		"tokensmith", "user-token", "create", "--enable-local-user-mint",
		"--key-file=/tmp/keys/private.pem", "--subject=pcs-test-caller",
		"--issuer=" + tokenSmithIssuer, "--audience=power-control", "--scopes=" + scope,
	}
	code, output, err := s.tokensmith.Exec(context.Background(), cmd, exec.Multiplexed())
	require.NoError(t, err)
	data, err := io.ReadAll(output)
	require.NoError(t, err)
	require.Equal(t, 0, code, "%s", data)
	return strings.TrimSpace(string(data))
}

func (s *IntegrationTestSuite) TestTokenSmithAuthentication() {
	t := s.T()
	pcs := s.startPCS(tokenSmithEnv())
	invalidTokens := []string{"", "not-a-jwt", s.fetchToken("test-caller")}
	for _, token := range invalidTokens {
		status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", token, nil)
		require.Equal(t, http.StatusUnauthorized, status)
	}
	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", s.mintTokenSmithToken("power-reader"), nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
}

func (s *IntegrationTestSuite) TestTokenSmithAuthorization() {
	t := s.T()
	pcs := s.startPCS(tokenSmithEnv())
	reader := s.mintTokenSmithToken("power-reader")
	status, body := httpRequest(t, http.MethodPost, pcs+"/power-cap/snapshot", reader, []byte(`{"xnames":["x0c0s0b0n0"]}`))
	require.Equal(t, http.StatusForbidden, status, "%s", body)
	var denial struct {
		Reason        string `json:"reason"`
		PolicyVersion string `json:"policy_version"`
	}
	require.NoError(t, json.Unmarshal(body, &denial))
	require.Equal(t, "policy_denied", denial.Reason)
	require.NotEmpty(t, denial.PolicyVersion)

	// Inbound TokenSmith and outbound Keycloak OAuth2 must work together.
	operator := s.mintTokenSmithToken("power-operator")
	assertSMDLookup(t, pcs, operator, "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestTokenSmithIssuerAndAudience() {
	token := s.mintTokenSmithToken("power-reader")
	mismatchedClaims := map[string]string{
		"AUTH_ISSUER":   "https://another-issuer.example",
		"AUTH_AUDIENCE": "another-service",
	}
	for name, value := range mismatchedClaims {
		s.Run(name, func() {
			env := tokenSmithEnv()
			env[name] = value
			pcs := s.startPCS(env)
			status, _ := httpRequest(s.T(), http.MethodGet, pcs+"/transitions", token, nil)
			require.Equal(s.T(), http.StatusUnauthorized, status)
		})
	}
}

func (s *IntegrationTestSuite) TestTokenSmithPublicHealthEndpoints() {
	pcs := s.startPCS(tokenSmithEnv())
	healthEndpoints := map[string]int{
		"/liveness":  http.StatusNoContent,
		"/readiness": http.StatusNoContent,
		"/health":    http.StatusOK,
	}
	for path, want := range healthEndpoints {
		s.Run(path, func() {
			status, body := httpRequest(s.T(), http.MethodGet, pcs+path, "", nil)
			require.Equal(s.T(), want, status, "%s", body)
		})
	}
}

func (s *IntegrationTestSuite) TestTokenSmithAuthorizationModes() {
	modes := []string{"shadow", "off"}
	for _, mode := range modes {
		s.Run(mode, func() {
			t := s.T()
			env := tokenSmithEnv()
			env["AUTHZ_MODE"] = mode
			pcs := s.startPCS(env)
			status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
			require.Equal(t, http.StatusUnauthorized, status)
			assertSMDLookup(t, pcs, s.mintTokenSmithToken("power-reader"), "x0c0s0b0n0")
		})
	}
}

func (s *IntegrationTestSuite) TestTokenSmithInvalidConfiguration() {
	invalidConfig := []struct {
		name, value, errorText string
	}{
		{"AUTH_PROVIDER", "invalid", "invalid auth-provider"},
		{"AUTH_ISSUER", "", "TokenSmith requires auth-issuer"},
		{"AUTHZ_MODE", "invalid", "invalid authz-mode"},
		{"AUTHZ_POLICY_PATH", "/missing-policy.csv", "configure TokenSmith authorization"},
		{"PCS_JWKS_URL", tokenSmithIssuer + "/missing-jwks", "initialize TokenSmith JWKS"},
	}
	for _, tc := range invalidConfig {
		s.Run(tc.name, func() {
			t := s.T()
			env := tokenSmithEnv()
			env[tc.name] = tc.value
			ctr := newContainer(t, testcontainers.ContainerRequest{
				Image:      s.pcsImage,
				Cmd:        []string{"power-control"},
				Env:        env,
				Networks:   []string{s.network},
				WaitingFor: wait.ForExit().WithExitTimeout(15 * time.Second),
			}, true)
			state, err := ctr.State(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, state.ExitCode)
			logs, err := ctr.Logs(context.Background())
			require.NoError(t, err)
			defer logs.Close()
			data, err := io.ReadAll(logs)
			require.NoError(t, err)
			require.Contains(t, string(data), tc.errorText)
		})
	}
}
