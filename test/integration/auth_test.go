//go:build integration_tests

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func (s *IntegrationTestSuite) TestSMDRequiresKeycloakToken() {
	t := s.T()
	invalidTokens := []string{"", "not-a-jwt"}
	for _, token := range invalidTokens {
		status, _ := httpRequest(t, http.MethodGet, s.smdURL+"/hsm/v2/State/Components/x0c0s0b0n0", token, nil)
		require.Equal(t, http.StatusUnauthorized, status)

		// These are the protected endpoints used by PCS's HSM lookup.
		status, _ = httpRequest(t, http.MethodPost, s.smdURL+"/hsm/v2/State/Components/Query", token, []byte(`{"ComponentIDs":["x0c0s0b0n0"]}`))
		require.Equal(t, http.StatusUnauthorized, status)

		status, _ = httpRequest(t, http.MethodGet, s.smdURL+"/hsm/v2/Inventory/ComponentEndpoints", token, nil)
		require.Equal(t, http.StatusUnauthorized, status)
	}

	status, body := httpRequest(t, http.MethodGet, s.smdURL+"/hsm/v2/State/Components/x0c0s0b0n0", s.fetchToken("test-caller"), nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	require.Contains(t, string(body), "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestNoAuthentication() {
	t := s.T()
	pcs := s.startPCS(map[string]string{
		"PCS_JWKS_URL":         "",
		"OAUTH2_CLIENT_ID":     "",
		"OAUTH2_CLIENT_SECRET": "",
		"OAUTH2_TOKEN_URL":     "",
		"OAUTH2_SCOPES":        "",
		"SMS_SERVER":           "http://smd-open:27779",
	})

	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	assertSMDLookup(t, pcs, "", "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestIncomingJWKSOnly() {
	t := s.T()
	pcs := s.startPCS(map[string]string{
		"PCS_JWKS_URL":         internalJWKS,
		"OAUTH2_CLIENT_ID":     "",
		"OAUTH2_CLIENT_SECRET": "",
		"OAUTH2_TOKEN_URL":     "",
		"OAUTH2_SCOPES":        "",
		"SMS_SERVER":           "http://smd-open:27779",
	})

	status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
	require.Equal(t, http.StatusUnauthorized, status)

	token := s.fetchToken("test-caller")
	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", token, nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	assertSMDLookup(t, pcs, token, "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestOutgoingOAuth2Only() {
	t := s.T()
	pcs := s.startPCS(map[string]string{
		"PCS_JWKS_URL": "",
	})

	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	assertSMDLookup(t, pcs, "", "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestJWKSAndOAuth2FromEnvironment() {
	t := s.T()
	pcs := s.startPCS(nil)

	status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
	require.Equal(t, http.StatusUnauthorized, status)

	token := s.fetchToken("test-caller")
	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", token, nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	assertSMDLookup(t, pcs, token, "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestIncomingAuthentication() {
	pcs := s.startPCS(nil)
	token := s.fetchToken("test-caller")
	tokenCases := []struct {
		name, token string
		status      int
	}{
		{
			name:   "valid",
			token:  token,
			status: http.StatusOK,
		},
		{
			name:   "missing",
			status: http.StatusUnauthorized,
		},
		{
			name:   "malformed",
			token:  "not-a-jwt",
			status: http.StatusUnauthorized,
		},
	}

	for _, tc := range tokenCases {
		s.Run(tc.name, func() {
			t := s.T()
			status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", tc.token, nil)
			require.Equal(t, tc.status, status, "GET /transitions: %s", body)

			status, body = httpRequest(t, http.MethodPost, pcs+"/power-cap/snapshot", tc.token, []byte(`{"xnames":["x0c0s0b0n0"]}`))
			require.Equal(t, tc.status, status, "POST /power-cap/snapshot: %s", body)
		})
	}
}

func (s *IntegrationTestSuite) TestPublicHealthEndpoints() {
	pcs := s.startPCS(nil)
	healthEndpoints := map[string]int{
		"/liveness":  http.StatusNoContent,
		"/readiness": http.StatusNoContent,
		"/health":    http.StatusOK,
	}
	for path, want := range healthEndpoints {
		s.Run(path, func() {
			t := s.T()
			status, body := httpRequest(t, http.MethodGet, pcs+path, "", nil)
			require.Equal(t, want, status, "%s", body)
		})
	}
}

func (s *IntegrationTestSuite) TestOAuth2FlagsOverrideEnvironment() {
	t := s.T()
	env := map[string]string{"PCS_JWKS_URL": ""}
	for k := range oauthEnv() {
		env[k] = "invalid-environment-value"
	}
	pcs := s.startPCS(env,
		"--jwks-url="+internalJWKS,
		"--oauth2-client-id=pcs-service",
		"--oauth2-client-secret=pcs-service-test-secret",
		"--oauth2-token-url="+internalTokenURL,
		"--oauth2-scopes=pcs-test",
	)
	status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", "", nil)
	require.Equal(t, http.StatusUnauthorized, status)
	assertSMDLookup(t, pcs, s.fetchToken("test-caller"), "x0c0s0b0n0")
}

func (s *IntegrationTestSuite) TestExpiredIncomingToken() {
	t := s.T()
	pcs := s.startPCS(nil)
	token := s.fetchToken("expired-caller")
	status, body := httpRequest(t, http.MethodGet, pcs+"/transitions", token, nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	waitFor(t, 10*time.Second, "expired token must be rejected", func() bool {
		status, _ := httpRequest(t, http.MethodGet, pcs+"/transitions", token, nil)
		return status == http.StatusUnauthorized
	})
}

func (s *IntegrationTestSuite) TestOutgoingTokenRenewal() {
	t := s.T()
	pcs := s.startPCS(map[string]string{"PCS_JWKS_URL": ""})
	assertSMDLookup(t, pcs, "", "x0c0s0b0n0")
	// When this newer token expires, PCS's original token has also expired.
	controlToken := s.fetchToken("pcs-service")
	waitFor(t, 45*time.Second, "SMD must reject the expired control token", func() bool {
		status, _ := httpRequest(t, http.MethodGet, s.smdURL+"/hsm/v2/State/Components/x0c0s0b0n0", controlToken, nil)
		return status == http.StatusUnauthorized
	})
	// A new component forces an SMD lookup instead of a cache hit.
	s.addSMDComponent("x0c0s0b0n1")
	assertSMDLookup(t, pcs, "", "x0c0s0b0n1")
}

func (s *IntegrationTestSuite) TestIncompleteOAuth2Configuration() {
	for missing := range oauthEnv() {
		s.Run(missing, func() {
			t := s.T()
			env := oauthEnv()
			delete(env, missing)
			ctr := newContainer(t, testcontainers.ContainerRequest{
				Image:      s.pcsImage,
				Cmd:        []string{"power-control"},
				Env:        env,
				Networks:   []string{s.network},
				WaitingFor: wait.ForExit().WithExitTimeout(10 * time.Second),
			}, true)
			state, err := ctr.State(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, state.ExitCode)
			logs, err := ctr.Logs(context.Background())
			require.NoError(t, err)
			defer logs.Close()
			var buf strings.Builder
			_, err = io.Copy(&buf, logs)
			require.NoError(t, err)
			require.Contains(t, buf.String(), "Incomplete OAuth2 configuration")
		})
	}
}

// These nodes deliberately have no Redfish endpoints. The exact domain error
// proves PCS found the node through SMD's protected state AND inventory APIs.
// Failed auth or a missing node produces a different result. No BMC is needed.
func assertSMDLookup(t *testing.T, pcs, token, xname string) {
	t.Helper()
	status, body := httpRequest(t, http.MethodPost, pcs+"/power-cap/snapshot", token, []byte(`{"xnames":["`+xname+`"]}`))
	require.Equal(t, http.StatusOK, status, "%s", body)

	var created struct {
		TaskID string `json:"taskID"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.TaskID)

	var task struct {
		Status     string `json:"taskStatus"`
		Components []struct {
			Xname string `json:"xname"`
			Error string `json:"error"`
		} `json:"components"`
	}

	waitFor(t, 20*time.Second, "power-cap task must complete", func() bool {
		status, body = httpRequest(t, http.MethodGet, pcs+"/power-cap/"+created.TaskID, token, nil)
		require.Equal(t, http.StatusOK, status, "%s", body)
		require.NoError(t, json.Unmarshal(body, &task))
		return task.Status == "completed"
	})
	require.Len(t, task.Components, 1)
	require.Equal(t, xname, task.Components[0].Xname)
	require.Equal(t, "Missing RfFQDN", task.Components[0].Error)
}
