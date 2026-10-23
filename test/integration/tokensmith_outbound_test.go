//go:build integration_tests

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func (s *IntegrationTestSuite) mintBootstrapToken() string {
	t := s.T()
	t.Helper()
	cmd := []string{
		"tokensmith", "bootstrap-token", "create",
		"--subject=power-control", "--audience=smd", "--scopes=read",
		"--ttl=10m", "--refresh-ttl=24h",
		"--bootstrap-store=/tmp/bootstrap", "--output-format=json",
	}
	code, output, err := s.tokensmith.Exec(context.Background(), cmd)
	require.NoError(t, err)
	// The CLI writes diagnostics to stderr alongside its JSON output.
	var stdout bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, io.Discard, output)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	var response struct {
		BootstrapToken string `json:"bootstrap_token"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &response))
	require.NotEmpty(t, response.BootstrapToken)
	return response.BootstrapToken
}

func (s *IntegrationTestSuite) TestTokenSmithOutbound() {
	// SMD must enforce TokenSmith signatures rather than accept unauthenticated
	// requests or tokens from the existing Keycloak provider.
	for _, token := range []string{"", s.fetchToken("test-caller")} {
		status, _ := httpRequest(s.T(), http.MethodGet,
			s.smdTokenSmithURL+"/hsm/v2/State/Components/x0c0s0b0n0", token, nil)
		require.Equal(s.T(), http.StatusUnauthorized, status)
	}
	for _, inbound := range []string{"jwks", "tokensmith"} {
		s.Run(inbound, func() {
			env := map[string]string{
				"SMD_AUTH_PROVIDER":          "tokensmith",
				"SMD_TOKENSMITH_URL":         tokenSmithIssuer,
				"TOKENSMITH_BOOTSTRAP_TOKEN": s.mintBootstrapToken(),
				"SMS_SERVER":                 "http://smd-tokensmith:27779",
			}
			token := s.fetchToken("test-caller")
			if inbound == "tokensmith" {
				for name, value := range tokenSmithEnv() {
					env[name] = value
				}
				token = s.mintTokenSmithToken("power-operator")
			}
			pcs := s.startPCS(env)
			assertSMDLookup(s.T(), pcs, token, "x0c0s0b0n0")
		})
	}
}

func (s *IntegrationTestSuite) TestTokenSmithOutboundInvalidConfiguration() {
	for _, tc := range []struct{ name, key, value, errorText string }{
		{"provider", "SMD_AUTH_PROVIDER", "invalid", "invalid smd-auth-provider"},
		{"url", "SMD_TOKENSMITH_URL", "", "valid smd-tokensmith-url"},
		{"credentials", "TOKENSMITH_BOOTSTRAP_TOKEN", "", "requires TOKENSMITH_BOOTSTRAP_TOKEN"},
		{"exchange", "TOKENSMITH_BOOTSTRAP_TOKEN", "invalid", "initialize outbound TokenSmith"},
	} {
		s.Run(tc.name, func() {
			t := s.T()
			env := map[string]string{
				"SMD_AUTH_PROVIDER": "tokensmith", "SMD_TOKENSMITH_URL": tokenSmithIssuer,
				"TOKENSMITH_BOOTSTRAP_TOKEN": "invalid",
			}
			env[tc.key] = tc.value
			ctr := newContainer(t, testcontainers.ContainerRequest{
				Image: s.pcsImage, Cmd: []string{"power-control"}, Env: env,
				Networks:   []string{s.network},
				WaitingFor: wait.ForExit().WithExitTimeout(45 * time.Second),
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
