package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/Cray-HPE/hms-certs/pkg/hms_certs"
	trsapi "github.com/Cray-HPE/hms-trs-app-api/v3/pkg/trs_http_api"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/openchami/power-control/v2/internal/logger"
)

// initHTTPClients loads trust once, before clients are shared with workers or
// reservations. Certificate changes require a restart.
func initHTTPClients(ctx context.Context, caURI string, rfTloc trsapi.TrsAPI, config *oauth2Config) (*hms_certs.HTTPClientPair, *hms_certs.HTTPClientPair, error) {
	var roots *x509.CertPool
	if caURI != "" {
		chain, err := hms_certs.FetchCAChain(caURI)
		if err != nil {
			return nil, nil, fmt.Errorf("load PCS_CA_URI %q: %w", caURI, err)
		}
		chain = hms_certs.TupleToNewline(chain)
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, nil, fmt.Errorf("load system CA certificates: %w", err)
		}
		if !roots.AppendCertsFromPEM([]byte(chain)) {
			return nil, nil, fmt.Errorf("PCS_CA_URI %q contains no valid CA certificates", caURI)
		}
		if err := rfTloc.SetSecurity(trsapi.TRSHTTPLocalSecurity{CACertBundleData: chain}); err != nil {
			return nil, nil, fmt.Errorf("configure Redfish TRS security: %w", err)
		}
	}

	rfClient, err := newHTTPClientPair(roots, true)
	if err != nil {
		return nil, nil, fmt.Errorf("create Redfish HTTP client: %w", err)
	}
	smdClient, err := newHTTPClientPair(roots, false)
	if err != nil {
		return nil, nil, fmt.Errorf("create SMD HTTP client: %w", err)
	}
	if config != nil {
		logger.Log.Info("Configuring SMD client with OAuth2")
		clientConfig := &clientcredentials.Config{
			ClientID:     config.clientID,
			ClientSecret: config.clientSecret,
			TokenURL:     config.tokenURL,
			Scopes:       config.scopes,
			AuthStyle:    oauth2.AuthStyleInHeader,
		}
		// Both SMD pair entries use the same client; wrap its transport once.
		client := smdClient.SecureClient.HTTPClient
		client.Transport = &oauth2.Transport{
			Source: clientConfig.TokenSource(ctx),
			Base:   client.Transport,
		}
	}
	return rfClient, smdClient, nil
}

func newHTTPClientPair(roots *x509.CertPool, allowInsecure bool) (*hms_certs.HTTPClientPair, error) {
	client, err := hms_certs.CreateRetryableInsecureHTTPClient(dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
	if err != nil {
		return nil, err
	}
	if roots != nil {
		client.HTTPClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	}
	// hms-certs skips failover when both entries point to the same client.
	// Without a CA this preserves legacy behavior.
	pair := &hms_certs.HTTPClientPair{SecureClient: client, InsecureClient: client}
	if roots != nil && allowInsecure {
		pair.InsecureClient, err = hms_certs.CreateRetryableInsecureHTTPClient(dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
		if err != nil {
			return nil, err
		}
	}
	return pair, nil
}
