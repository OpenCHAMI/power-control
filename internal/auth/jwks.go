// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/OpenCHAMI/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v3/jwk"
	openchami_authenticator "github.com/openchami/chi-middleware/auth"

	"github.com/openchami/power-control/v2/internal/logger"
)

// JWKSConfig configures JWT authentication using a JWKS endpoint.
type JWKSConfig struct {
	JWKSURL string
}

type jwksAuth struct {
	tokenAuth *jwtauth.JWTAuth
}

func NewJWKSAuth(ctx context.Context, config JWKSConfig) (Auth, error) {
	var tokenAuth *jwtauth.JWTAuth
	if config.JWKSURL != "" {
		var err error
		logger.Log.Info("Fetching public key from server...")
		for i := 0; i <= 5; i++ {
			tokenAuth, err = fetchPublicKeyFromURL(ctx, config.JWKSURL)
			if err != nil {
				logger.Log.Errorf("Failed to initialize auth token: %v", err)
				if i == 5 {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
				}
				continue
			}
			logger.Log.Info("Initialized the auth token successfully.")
			break
		}
		if err != nil {
			return nil, fmt.Errorf("initialize JWKS: %w", err)
		}
	}

	return &jwksAuth{tokenAuth: tokenAuth}, nil
}

func (a *jwksAuth) Wrap(next http.Handler) http.Handler {
	if a.tokenAuth == nil {
		return next
	}
	authenticate := openchami_authenticator.AuthenticatorWithRequiredClaims(
		a.tokenAuth,
		[]string{"sub", "iss", "aud"},
	)
	return jwtauth.Verifier(a.tokenAuth)(authenticate(next))
}

type statusCheckTransport struct {
	http.RoundTripper
}

func (ct *statusCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	return resp, err
}

func newHTTPClient() *http.Client {
	return &http.Client{Transport: &statusCheckTransport{}}
}

func fetchPublicKeyFromURL(ctx context.Context, url string) (*jwtauth.JWTAuth, error) {
	client := newHTTPClient()

	set, err := jwk.Fetch(ctx, url, jwk.WithHTTPClient(client))
	if err != nil {
		msg := "%w"

		// if the error tree contains an EOF, it means that the response was empty,
		// so add a more descriptive message to the error tree
		if errors.Is(err, io.EOF) {
			msg = "received empty response for key: %w"
		}

		return nil, fmt.Errorf(msg, err)
	}
	jwks, err := json.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JWKS: %v", err)
	}
	tokenAuth, err := jwtauth.NewKeySet(jwks)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize JWKS: %v", err)
	}

	return tokenAuth, nil
}
