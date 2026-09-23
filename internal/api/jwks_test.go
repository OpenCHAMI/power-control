package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/openchami/power-control/v2/internal/logger"
)

func TestNewJWKSAuthFailsClosed(t *testing.T) {
	logger.Init()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("invalid JWKS"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	auth, err := NewJWKSAuth(ctx, JWKSConfig{JWKSURL: server.URL})
	require.ErrorContains(t, err, "initialize JWKS")
	require.Nil(t, auth)
}
