//go:build !integration_tests

package domain

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/openchami/power-control/v2/internal/logger"
	pcsmodel "github.com/openchami/power-control/v2/internal/model"
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

func TestJawsLoadMarksOutletsUnavailableOnPollFailure(t *testing.T) {
	tests := []struct {
		name      string
		roundTrip func(*http.Request) (*http.Response, error)
	}{
		{
			name: "transport error",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
		},
		{
			name: "HTTP error",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     "400 Bad Request",
					Body:       http.NoBody,
				}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupPendingSMDTest(t, func([]string, string) error { return nil })

			previousLogger := logger.Log
			logger.Log = logrus.New()
			t.Cleanup(func() { logger.Log = previousLogger })

			client := jawsHTTPClient()
			previousTransport := client.HTTPClient.Transport
			previousCheckRetry := client.CheckRetry
			client.HTTPClient.Transport = refreshTransport(tt.roundTrip)
			client.CheckRetry = func(context.Context, *http.Response, error) (bool, error) {
				return false, nil
			}
			t.Cleanup(func() {
				client.HTTPClient.Transport = previousTransport
				client.CheckRetry = previousCheckRetry
			})

			otherOutlet := "x0m1p0v1"
			hwStateMap[otherOutlet] = &componentPowerInfo{
				PSComp: pcsmodel.PowerStatusComponent{
					XName: otherOutlet, PowerState: "on", ManagementState: "available",
				},
			}

			JawsLoad("x0m0", "pdu.example", "", "")

			require.Equal(t, "undefined", hwStateMap["x0m0p0v1"].PSComp.PowerState)
			require.Equal(t, "unavailable", hwStateMap["x0m0p0v1"].PSComp.ManagementState)
			require.Equal(t, "on", hwStateMap[otherOutlet].PSComp.PowerState)
			require.Equal(t, "available", hwStateMap[otherOutlet].PSComp.ManagementState)
		})
	}
}
