package domain

import (
	"net/url"
	"os"
	"strings"
)

const (
	protoEnv     = "PCS_DEBUG_RFE_PROTO"
	rfeHostEnv   = "PCS_DEBUG_RFE_HOST"
	rfeDomainEnv = "PCS_DEBUG_RFE_DOMAIN"
)

func getPowerURL(host, path string) string {
	proto := "https"
	if p, exists := os.LookupEnv(protoEnv); exists {
		proto = p
	}
	if h, exists := os.LookupEnv(rfeHostEnv); exists {
		host = h
	}
	segments := []string{host}
	if d, exists := os.LookupEnv(rfeDomainEnv); exists {
		segments = append(segments, d)
	}
	fqdn := strings.Join(segments, ".")

	u := &url.URL{
		Scheme: proto,
		Host:   fqdn,
		Path:   path,
	}

	return u.String()
}
