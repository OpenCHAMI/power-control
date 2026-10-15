// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/openchami/tokensmith/pkg/authn"
	"github.com/openchami/tokensmith/pkg/authz"
	"github.com/openchami/tokensmith/pkg/authz/engine"
	"github.com/openchami/tokensmith/pkg/authz/presets"
	"github.com/openchami/tokensmith/pkg/keys"

	"github.com/openchami/power-control/v2/internal/logger"
)

// TokensmithConfig configures inbound authentication and authorization only.
type TokensmithConfig struct {
	JWKSURL      string
	Issuer       string
	Audience     string
	Mode         string
	ModelPath    string
	PolicyPath   string
	GroupingPath string
}

type tokenSmithAuth struct {
	authenticate func(http.Handler) http.Handler
	authorize    *authz.Middleware
}

func NewTokenSmithAuth(ctx context.Context, config TokensmithConfig) (Auth, error) {
	mode := authz.Mode(config.Mode)
	switch mode {
	case authz.ModeOff, authz.ModeShadow, authz.ModeEnforce:
	default:
		return nil, fmt.Errorf("invalid tokensmith-authz-mode %q: use off, shadow, or enforce", config.Mode)
	}
	if config.Issuer == "" || config.Audience == "" || config.JWKSURL == "" {
		return nil, fmt.Errorf("TokenSmith requires tokensmith-issuer, tokensmith-audience, and tokensmith-jwks-url")
	}
	if mode != authz.ModeOff && config.PolicyPath == "" {
		return nil, fmt.Errorf("TokenSmith requires tokensmith-authz-policy-path in %s mode", mode)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	authenticate, err := authn.Middleware(authn.Options{
		Issuers:    []string{config.Issuer},
		Audiences:  []string{config.Audience},
		JWKSURLs:   []string{config.JWKSURL},
		HTTPClient: client,
		Mapper:     tokenSmithPrincipal,
	})
	if err != nil {
		return nil, fmt.Errorf("configure TokenSmith authentication: %w", err)
	}

	var authorizer *authz.Authorizer
	if mode != authz.ModeOff {
		builder := engine.NewBuilder().
			WithModelPreset(presets.RBACKeyMatch2REST()).
			WithPolicyPath(config.PolicyPath).
			WithGroupingPath(config.GroupingPath).
			WithAuthorizerOptions(authz.WithDecisionCacheFromEnv())
		if config.ModelPath != "" {
			builder.WithModelPath(config.ModelPath)
		}
		authorizer, err = builder.Build()
		if err != nil {
			return nil, fmt.Errorf("configure TokenSmith authorization: %w", err)
		}
	}

	// TokenSmith fetches JWKS lazily; check them before starting PCS as well.
	if err := checkTokenSmithJWKS(ctx, client, config.JWKSURL); err != nil {
		return nil, fmt.Errorf("initialize TokenSmith JWKS: %w", err)
	}

	authorize := authz.NewMiddleware(authorizer,
		authz.PathMethodMapper{MethodToAction: authz.MethodToActionREST()},
		authz.WithMode(mode),
		authz.WithRequireAuthn(true),
		authz.WithAllowUnmapped(false),
		authz.WithRequestIDFromContext(middleware.GetReqID),
	)
	authorize.OnDecision = func(_ context.Context, decision authz.DecisionRecord) {
		entry := logger.Log.WithField("decision", decision)
		if decision.Decision == authz.DecisionAllow {
			entry.Debug("TokenSmith authorization")
		} else {
			entry.Info("TokenSmith authorization")
		}
	}
	logger.Log.WithField("authz_mode", config.Mode).
		WithField("policy_source", config.PolicyPath).
		WithField("policy_version", authorizer.PolicyVersion()).
		Info("TokenSmith authentication configured")

	return &tokenSmithAuth{
		authenticate: authenticate,
		authorize:    authorize,
	}, nil
}

func (a *tokenSmithAuth) Wrap(next http.Handler) http.Handler {
	return a.authenticate(a.authorize.Handler(next))
}

func tokenSmithPrincipal(_ context.Context, _ *jwt.Token, claims jwt.MapClaims) (authz.Principal, error) {
	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return authz.Principal{}, fmt.Errorf("missing subject")
	}
	principal := authz.Principal{ID: subject}
	// TokenSmith exposes its granted roles in the scope array.
	if scopes, ok := claims["scope"].([]any); ok {
		for _, scope := range scopes {
			if role, ok := scope.(string); ok {
				principal.Roles = append(principal.Roles, role)
			}
		}
	}
	return principal, nil
}

func checkTokenSmithJWKS(ctx context.Context, client *http.Client, url string) error {
	set, err := jwk.Fetch(ctx, url, jwk.WithHTTPClient(client))
	if err != nil {
		return err
	}
	for i := range set.Len() {
		key, _ := set.Key(i)
		kid, _ := key.KeyID()
		if !keys.IsRFC7638Thumbprint(kid) {
			continue
		}
		if algorithm, ok := key.Algorithm(); ok && keys.ValidateAlgorithm(algorithm.String()) != nil {
			continue
		}
		publicKey, err := jwk.PublicRawKeyOf(key)
		if err != nil {
			continue
		}
		switch publicKey.(type) {
		case *rsa.PublicKey, *ecdsa.PublicKey:
			return nil
		}
	}
	return fmt.Errorf("JWKS contains no usable TokenSmith verification keys")
}
