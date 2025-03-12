package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/OpenCHAMI/jwtauth/v5"
	"github.com/lestrrat-go/jwx/jwk"
)

type statusCheckTransport struct {
	http.RoundTripper
}

func newHTTPClient() *http.Client {
	return &http.Client{Transport: &statusCheckTransport{}}
}

func FetchPublicKeyFromURL(url string) error {
	client := newHTTPClient()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set, err := jwk.Fetch(ctx, url, jwk.WithHTTPClient(client))
	if err != nil {
		msg := "%w"

		// if the error tree contains an EOF, it means that the response was empty,
		// so add a more descriptive message to the error tree
		if errors.Is(err, io.EOF) {
			msg = "received empty response for key: %w"
		}

		return fmt.Errorf(msg, err)
	}
	jwks, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("failed to marshal JWKS: %v", err)
	}
	tokenAuth, err = jwtauth.NewKeySet(jwks)
	if err != nil {
		return fmt.Errorf("failed to initialize JWKS: %v", err)
	}

	return nil
}
