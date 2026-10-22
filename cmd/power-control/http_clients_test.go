package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cray-HPE/hms-certs/pkg/hms_certs"
	trsapi "github.com/Cray-HPE/hms-trs-app-api/v3/pkg/trs_http_api"
	reservation "github.com/openchami/smd/v2/pkg/service-reservations"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/openchami/power-control/v2/internal/hsm"
	"github.com/openchami/power-control/v2/internal/logger"
)

// Each server has its own certificate, so trusting one cannot trust the other.
func newTLSTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func writeTestCABundle(t *testing.T, chain string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, []byte(chain), 0600))
	return path
}

func newTestHTTPClients(t *testing.T, caURI string, config *oauth2Config) (*hms_certs.HTTPClientPair, *hms_certs.HTTPClientPair, *trsapi.TRSHTTPLocal) {
	t.Helper()
	tloc := &trsapi.TRSHTTPLocal{Logger: logrus.New()}
	rfClient, smdClient, err := initHTTPClients(context.Background(), caURI, tloc, config)
	require.NoError(t, err)
	t.Cleanup(rfClient.CloseIdleConnections)
	t.Cleanup(smdClient.CloseIdleConnections)
	return rfClient, smdClient, tloc
}

func TestHTTPClientsTRSTrust(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	trusted := newTLSTestServer(t, handler)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: trusted.Certificate().Raw})

	encodings := []string{"PEM", "Vault tuple"}
	for _, encoding := range encodings {
		t.Run(encoding, func(t *testing.T) {
			chain := string(caPEM)
			if encoding == "Vault tuple" {
				chain = hms_certs.NewlineToTuple(chain)
			}
			path := writeTestCABundle(t, chain)
			_, _, tloc := newTestHTTPClients(t, path, nil)
			require.NotNil(t, tloc.CACertPool)
			_, err := trusted.Certificate().Verify(x509.VerifyOptions{Roots: tloc.CACertPool})
			require.NoError(t, err, "TRS must trust the loaded CA before workers start")
		})
	}
}

func TestHTTPClientsTrust(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	trusted := newTLSTestServer(t, handler)
	untrusted := newTLSTestServer(t, handler)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: trusted.Certificate().Raw})
	path := writeTestCABundle(t, string(caPEM))
	rfClient, smdClient, _ := newTestHTTPClients(t, path, nil)
	testCases := []struct {
		name    string
		client  *hms_certs.HTTPClientPair
		url     string
		wantErr bool
	}{
		{"SMD trusted", smdClient, trusted.URL, false},
		{"SMD untrusted", smdClient, untrusted.URL, true},
		{"Redfish trusted", rfClient, trusted.URL, false},
		{"Redfish fallback", rfClient, untrusted.URL, false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			require.NoError(t, err)
			rsp, err := tc.client.Do(req)
			if rsp != nil {
				defer rsp.Body.Close()
			}
			if tc.wantErr {
				require.ErrorContains(t, err, "certificate")
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, rsp.StatusCode)
		})
	}
}

func TestHTTPClientsKeepStartupTrust(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	trusted := newTLSTestServer(t, handler)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: trusted.Certificate().Raw})
	path := writeTestCABundle(t, string(caPEM))
	_, smdClient, _ := newTestHTTPClients(t, path, nil)

	// Trust is fixed at startup, even if the source bundle changes.
	require.NoError(t, os.WriteFile(path, []byte("invalid replacement"), 0600))
	req, err := http.NewRequest(http.MethodGet, trusted.URL, nil)
	require.NoError(t, err)
	rsp, err := smdClient.Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
}

func TestHTTPClientsRejectInvalidCA(t *testing.T) {
	testCases := []struct {
		name, contents string
	}{
		{"missing", ""},
		{"empty", ""},
		{"non-PEM", "not a certificate"},
		{"malformed PEM", "-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ca.pem")
			if tc.name != "missing" {
				require.NoError(t, os.WriteFile(path, []byte(tc.contents), 0600))
			}
			tloc := &trsapi.TRSHTTPLocal{Logger: logrus.New()}
			rfClient, smdClient, err := initHTTPClients(context.Background(), path, tloc, nil)
			require.ErrorContains(t, err, "PCS_CA_URI")
			require.Nil(t, rfClient)
			require.Nil(t, smdClient)
		})
	}
}

func TestPCSRejectsInvalidCAAtStartup(t *testing.T) {
	logger.Init()
	t.Setenv("PCS_JWKS_URL", "")
	t.Setenv("TRS_IMPLEMENTATION", "LOCAL")
	t.Setenv("PCS_CA_URI", filepath.Join(t.TempDir(), "missing.pem"))
	err := runPCS(context.Background(), &pcsConfig{authProvider: authProviderJWKS}, nil, nil, nil)
	require.ErrorContains(t, err, "PCS_CA_URI")
}

func TestHTTPClientsSMDAuthentication(t *testing.T) {
	logger.Init()
	var tokenRequests atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokens.Close()
	var smdRequests atomic.Int32
	smd := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		smdRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Success":[{"ID":"x0c0s0b0n0","DeputyKey":"deputy"}]}`)
	}))
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: smd.Certificate().Raw})
	path := writeTestCABundle(t, string(caPEM))

	testCases := []struct {
		name, caURI string
	}{
		{"verified TLS", path},
		{"legacy TLS", ""},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, smdClient, _ := newTestHTTPClients(t, tc.caURI, &oauth2Config{
				tokenURL: tokens.URL, clientID: "pcs", clientSecret: "secret",
			})
			h := &hsm.HSMv2{}
			require.NoError(t, h.Init(&hsm.HSM_GLOBALS{
				SvcName: "test", Logger: logrus.New(), LockEnabled: true,
				SMUrl: smd.URL, SVCHttpClient: smdClient,
			}))
			t.Run("SMD request", func(t *testing.T) {
				require.NoError(t, h.Ping())
			})
			t.Run("reservation", func(t *testing.T) {
				keys := []reservation.Key{{ID: "x0c0s0b0n0", Key: "deputy"}}
				result, err := h.HSMGlobals.Reservation.ValidateDeputyKeys(keys)
				require.NoError(t, err, "reservations must use the configured authenticated client")
				require.Len(t, result.Success, 1)
			})
		})
	}
	require.EqualValues(t, 4, smdRequests.Load())
	require.EqualValues(t, 2, tokenRequests.Load(), "each client should reuse its token")
}
