//go:build integration_tests

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	keycloak "github.com/stillya/testcontainers-keycloak"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	realmPath        = "/realms/pcs-auth-test/protocol/openid-connect"
	internalJWKS     = "http://keycloak:8080" + realmPath + "/certs"
	internalTokenURL = "http://keycloak:8080" + realmPath + "/token"
	smdImage         = "ghcr.io/openchami/smd:v2.20.4"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// newContainer registers cleanup even when startup fails and collects logs
// before termination, including failures in nested tests.
func newContainer(t *testing.T, req testcontainers.ContainerRequest, start bool) testcontainers.Container {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          start,
	})

	testcontainers.CleanupContainer(t, ctr)
	if ctr != nil {
		t.Cleanup(func() {
			if !t.Failed() {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			logs, err := ctr.Logs(ctx)
			if err != nil {
				t.Logf("container logs: %v", err)
				return
			}

			defer logs.Close()
			data, _ := io.ReadAll(logs)
			t.Logf("%s logs:\n%s", req.Image, data)
		})
	}

	require.NoError(t, err)

	return ctr
}

func containerEndpoint(t *testing.T, ctr testcontainers.Container, port string) string {
	t.Helper()
	endpoint, err := ctr.PortEndpoint(context.Background(), port, "http")
	require.NoError(t, err)

	return endpoint
}

// IntegrationTestSuite owns shared services; each test starts its own PCS instance.
type IntegrationTestSuite struct {
	suite.Suite
	network     string
	pcsImage    string
	keycloakURL string
	smdURL      string
	tokensmith  testcontainers.Container
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (s *IntegrationTestSuite) SetupSuite() {
	t := s.T()
	net, err := network.New(context.Background())
	testcontainers.CleanupNetwork(t, net)
	require.NoError(t, err)
	s.network = net.Name
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

	// Build PCS once using the release Dockerfile and CGO setting.
	cgoEnabled := "1"
	var buildLogs bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("PCS image build:\n%s", buildLogs.String())
		}
	})
	imageRequest := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:        root,
			Dockerfile:     "Dockerfile.build",
			BuildLogWriter: &buildLogs,
			BuildArgs: map[string]*string{
				"CGO_ENABLED": &cgoEnabled,
			},
			BuildOptionsModifier: func(opts *client.ImageBuildOptions) {
				opts.Version = build.BuilderBuildKit
			},
		},
	}
	pcs := newContainer(t, imageRequest, false)
	info, err := pcs.Inspect(context.Background())
	require.NoError(t, err)
	s.pcsImage = info.Config.Image

	kc, err := keycloak.Run(context.Background(), "quay.io/keycloak/keycloak:26.7.4",
		keycloak.WithRealmImportFile("testdata/realm.json"),
		testcontainers.WithEnv(map[string]string{
			"KC_HOSTNAME": "http://keycloak:8080",
		}),
		network.WithNetwork([]string{"keycloak"}, net),
		testcontainers.WithWaitStrategyAndDeadline(2*time.Minute,
			wait.ForHTTP(realmPath+"/certs").
				WithPort("8080/tcp").
				WithStartupTimeout(2*time.Minute),
		),
	)
	testcontainers.CleanupContainer(t, kc)
	require.NoError(t, err)
	s.keycloakURL = containerEndpoint(t, kc, "8080/tcp")

	db, err := postgres.Run(context.Background(), "postgres:18-alpine",
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.WithDatabase("hmsds"),
		postgres.BasicWaitStrategies(),
		network.WithNetwork([]string{"postgres"}, net),
	)
	testcontainers.CleanupContainer(t, db)
	require.NoError(t, err)

	init := newContainer(t, testcontainers.ContainerRequest{
		Image:      smdImage,
		Cmd:        []string{"/smd-init"},
		Env:        smdEnv(),
		Networks:   []string{s.network},
		WaitingFor: wait.ForExit().WithExitTimeout(time.Minute),
	}, true)
	state, err := init.State(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, state.ExitCode, "SMD migrations failed")

	s.smdURL = s.startSMD("smd", true)
	s.startSMD("smd-open", false)

	s.addSMDComponent("x0c0s0b0n0")
	s.startTokenSmith()
}

func smdEnv() map[string]string {
	return map[string]string{
		"SMD_DBHOST":       "postgres",
		"SMD_DBPORT":       "5432",
		"SMD_DBNAME":       "hmsds",
		"SMD_DBUSER":       "postgres",
		"SMD_DBPASS":       "postgres",
		"SMD_DBOPTS":       "sslmode=disable",
		"SMD_WVAULT":       "false",
		"SMD_RVAULT":       "false",
		"SMD_SLS_HOST":     "",
		"SMD_HBTD_HOST":    "",
		"ENABLE_DISCOVERY": "false",
	}
}

func (s *IntegrationTestSuite) startSMD(alias string, auth bool) string {
	t := s.T()
	t.Helper()
	env := smdEnv()
	if auth {
		env["SMD_JWKS_URL"] = internalJWKS
	}

	ctr := newContainer(t, testcontainers.ContainerRequest{
		Image:          smdImage,
		Env:            env,
		Networks:       []string{s.network},
		NetworkAliases: map[string][]string{s.network: {alias}},
		ExposedPorts:   []string{"27779/tcp"},
		WaitingFor:     wait.ForHTTP("/hsm/v2/service/ready").WithPort("27779/tcp").WithStartupTimeout(time.Minute),
	}, true)

	return containerEndpoint(t, ctr, "27779/tcp")
}

func (s *IntegrationTestSuite) startPCS(extra map[string]string, args ...string) string {
	t := s.T()
	t.Helper()
	env := map[string]string{
		"STORAGE":                   "MEMORY",
		"VAULT_ENABLED":             "false",
		"HSMLOCK_ENABLED":           "true",
		"TRS_IMPLEMENTATION":        "LOCAL",
		"SMS_SERVER":                "http://smd:27779",
		"PCS_POWER_SAMPLE_INTERVAL": "1",
		"LOG_LEVEL":                 "INFO",
		"PCS_JWKS_URL":              internalJWKS,
	}

	maps.Copy(env, oauthEnv())
	maps.Copy(env, extra)

	ctr := newContainer(t, testcontainers.ContainerRequest{
		Image:        s.pcsImage,
		Cmd:          append([]string{"power-control"}, args...),
		Env:          env,
		Networks:     []string{s.network},
		ExposedPorts: []string{"28007/tcp"},
		WaitingFor:   wait.ForHTTP("/liveness").WithPort("28007/tcp").WithStatusCodeMatcher(func(code int) bool { return code == http.StatusNoContent }).WithStartupTimeout(time.Minute),
	}, true)

	return containerEndpoint(t, ctr, "28007/tcp")
}

func oauthEnv() map[string]string {
	return map[string]string{
		"OAUTH2_CLIENT_ID":     "pcs-service",
		"OAUTH2_CLIENT_SECRET": "pcs-service-test-secret",
		"OAUTH2_TOKEN_URL":     internalTokenURL,
		"OAUTH2_SCOPES":        "pcs-test",
	}
}

func (s *IntegrationTestSuite) fetchToken(client string) string {
	t := s.T()
	t.Helper()
	secret := client + "-secret"
	if client == "pcs-service" {
		secret = "pcs-service-test-secret"
	}

	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"pcs-test"},
	}
	req, err := http.NewRequest(http.MethodPost, s.keycloakURL+realmPath+"/token", strings.NewReader(form.Encode()))
	require.NoError(t, err)

	req.SetBasicAuth(client, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var token struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&token))
	require.NotEmpty(t, token.AccessToken)

	return token.AccessToken
}

func httpRequest(t *testing.T, method, url, token string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")

	}
	resp, err := httpClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, data
}

func (s *IntegrationTestSuite) addSMDComponent(xname string) {
	t := s.T()
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"Components": []map[string]any{
			{
				"ID":      xname,
				"State":   "Ready",
				"Flag":    "OK",
				"Enabled": true,
				"Role":    "Compute",
			},
		},
	})
	require.NoError(t, err)

	status, data := httpRequest(t, http.MethodPost, s.smdURL+"/hsm/v2/State/Components", s.fetchToken("test-caller"), body)
	require.Equal(t, http.StatusNoContent, status, "%s", data)
}

// Poll in the test goroutine so assertions in check can safely call FailNow.
func waitFor(t *testing.T, timeout time.Duration, message string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal(message)
}
