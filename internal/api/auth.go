package api

import "net/http"

// Auth protects handlers using an initialized authentication provider.
type Auth interface {
	Wrap(http.Handler) http.Handler
}
