package domain

import (
	"os"
	"testing"
)

func TestGetPowerURL(t *testing.T) {
	cases := []struct {
		Host      string
		Path      string
		ProtoEnv  string
		HostEnv   string
		DomainEnv string
		Expect    string
	}{
		{
			Host:      "example",
			Path:      "/foo/bar",
			ProtoEnv:  "http",
			HostEnv:   "override",
			DomainEnv: "power.example",
			Expect:    "http://override.power.example/foo/bar",
		},
		{
			Host:   "example",
			Path:   "/foo/bar",
			Expect: "https://example/foo/bar",
		},
		{
			Host:      "example",
			Path:      "foo/bar",
			HostEnv:   "override",
			DomainEnv: "power.example",
			Expect:    "https://override.power.example/foo/bar",
		},
		{
			Host:    "example",
			Path:    "/foo/bar",
			HostEnv: "override",
			Expect:  "https://override/foo/bar",
		},
	}

	for _, c := range cases {
		os.Unsetenv(protoEnv)
		os.Unsetenv(rfeHostEnv)
		os.Unsetenv(rfeDomainEnv)
		if c.ProtoEnv != "" {
			os.Setenv(protoEnv, c.ProtoEnv)
		}
		if c.HostEnv != "" {
			os.Setenv(rfeHostEnv, c.HostEnv)
		}
		if c.DomainEnv != "" {
			os.Setenv(rfeDomainEnv, c.DomainEnv)
		}
		got := getPowerURL(c.Host, c.Path)
		if got != c.Expect {
			t.Errorf("bad match, got: '%s', want: '%s'", got, c.Expect)
		}
	}
}
