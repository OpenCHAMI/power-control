//go:build !integration_tests

package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJaws2xname(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		want     string
		wantOkay bool
	}{
		{name: "valid uppercase outlet ID", id: "A_17", want: "x3000m0p0v17", wantOkay: true},
		{name: "valid lowercase phase", id: "b_2", want: "x3000m0p1v2", wantOkay: true},
		{name: "empty ID", id: "", wantOkay: false},
		{name: "short ID", id: "A_", wantOkay: false},
		{name: "invalid phase", id: "1_2", wantOkay: false},
		{name: "invalid outlet number", id: "A_x", wantOkay: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, okay := jaws2xname("x3000m0", tt.id)
			require.Equal(t, tt.wantOkay, okay)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsJawsURI(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		{uri: "/jaws", want: true},
		{uri: "/jaws/monitor/outlets", want: true},
		{uri: "https://pdu.example/redfish/v1/jaws/control", want: true},
		{uri: "/redfish/v1/jawsomething", want: false},
		{uri: "/redfish/v1/Systems/1", want: false},
		{uri: "://invalid", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			require.Equal(t, tt.want, isJawsURI(tt.uri))
		})
	}
}
