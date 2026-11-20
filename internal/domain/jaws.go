// MIT License
//
// (C) Copyright [2025] Hewlett Packard Enterprise Development LP
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.

package domain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cray-HPE/hms-certs/pkg/hms_certs"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"

	"github.com/openchami/power-control/v2/internal/logger"
	pcsmodel "github.com/openchami/power-control/v2/internal/model"
)

type JawsEndpointStatus struct {
	Id                  string  `json:"id"`
	Name                string  `json:"name"`
	Active_power        int     `json:"active_power"`
	Active_power_status string  `json:"active_power_status"`
	Apparent_power      int     `json:"apparent_power"`
	Branch_id           string  `json:"branch_id"`
	Control_state       string  `json:"control_state"`
	Current             float32 `json:"current"`
	Current_capacity    int     `json:"current_capacity"`
	Current_status      string  `json:"current_status"`
	Current_utilized    float32 `json:"current_utilized"`
	Energy              int     `json:"energy"`
	Ocp_id              string  `json:"ocp_id"`
	Phase_id            string  `json:"phase_id"`
	Power_capacity      int     `json:"power_capacity"`
	Power_factor_status string  `json:"power_factor_status"`
	Socket_adapter      string  `json:"socket_adapter"`
	Socket_type         string  `json:"socket_type"`
	State               string  `json:"state"`
	Status              string  `json:"status"`
	Voltage             float32 `json:"voltage"`
}

// jawsSuites returns the TLS cipher suites to offer when connecting to
// JAWS PDUs: the legacy RSA key-exchange suites followed by Go's default
// suites.
//
// Older ServerTech JAWS PDU firmware only supports TLS 1.2 with RSA
// key-exchange cipher suites (TLS_RSA_WITH_*, no forward secrecy).  Go
// removed those suites from its client defaults in Go 1.22, and the
// GODEBUG=tlsrsakex opt-in was removed in Go 1.27, so without listing
// them explicitly here the TLS handshake with such PDUs fails with
// "remote error: tls: handshake failure".  The modern default suites are
// included as well so PDUs with newer firmware can still negotiate
// forward-secret (ECDHE) ciphers.
//
// trsCipherSuites() in internal/taskrun/trshttp_local.go does the same
// for the TRS clients used for power status/transitions/capping.  These
// legacy suites can be dropped once all supported PDU firmware offers
// ECDHE cipher suites.
func jawsSuites() []uint16 {
	suites := []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384, tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA}
	for _, c := range tls.CipherSuites() {
		suites = append(suites, c.ID)
	}
	return suites
}

// Determine whether to skip TLS certificate verification for JAWS
// connections. Defaults to false (certificates are verified) unless
// PCS_JAWS_INSECURE_SKIP_VERIFY is set to a valid boolean true value;
// invalid values fail closed.  The shipped Dockerfiles set it to true
// because current PDU firmware requires insecure transports.
func jawsInsecureSkipVerify() bool {
	skipVerify := false
	envstr := os.Getenv("PCS_JAWS_INSECURE_SKIP_VERIFY")
	if envstr != "" {
		yn, err := strconv.ParseBool(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_JAWS_INSECURE_SKIP_VERIFY, defaulting to %t",
				skipVerify)
		} else {
			skipVerify = yn
		}
	}
	return skipVerify
}

var jawsTLSOnce sync.Once
var jawsTLSConfig *tls.Config

// Build the shared TLS configuration for JAWS connections.  When
// certificate verification is enabled (PCS_JAWS_INSECURE_SKIP_VERIFY=false),
// PDU certificates are verified against the system CA certificates plus the
// CA chain referenced by PCS_CA_URI, if set -- the same CA source used for
// the Redfish HTTP clients.
func jawsTLS() *tls.Config {
	jawsTLSOnce.Do(func() {
		jawsTLSConfig = &tls.Config{
			InsecureSkipVerify: jawsInsecureSkipVerify(),
			CipherSuites:       jawsSuites(),
		}
		if jawsTLSConfig.InsecureSkipVerify {
			return
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			logger.Log.Errorf("JAWS: unable to load system CA certificates: %v", err)
			roots = x509.NewCertPool()
		}
		caURI := os.Getenv("PCS_CA_URI")
		if caURI != "" {
			chain, err := hms_certs.FetchCAChain(caURI)
			if err != nil {
				logger.Log.Errorf("JAWS: unable to load CA chain from PCS_CA_URI %q: %v",
					caURI, err)
			} else if !roots.AppendCertsFromPEM([]byte(hms_certs.TupleToNewline(chain))) {
				logger.Log.Errorf("JAWS: PCS_CA_URI %q contains no valid CA certificates",
					caURI)
			} else {
				logger.Log.Infof("JAWS: verifying PDU certificates against PCS_CA_URI %q and system CAs",
					caURI)
			}
		} else {
			logger.Log.Infof("JAWS: verifying PDU certificates against system CAs")
		}
		jawsTLSConfig.RootCAs = roots
	})
	return jawsTLSConfig
}

var jawsClientOnce sync.Once
var jawsClient *retryablehttp.Client

// jawsHTTPClient returns the shared retryable HTTP client used for all JAWS
// PDU requests.  A single client/transport is reused across polls so TLS
// keep-alive connections are pooled and reaped after an idle timeout rather
// than leaked by a throwaway transport on every poll.
func jawsHTTPClient() *retryablehttp.Client {
	jawsClientOnce.Do(func() {
		transport := &http.Transport{
			TLSClientConfig: jawsTLS(),
			IdleConnTimeout: 90 * time.Second,
		}
		jawsClient = retryablehttp.NewClient()
		jawsClient.HTTPClient.Transport = transport
	})
	return jawsClient
}

var jawsMonitorEnabledOnce sync.Once
var jawsMonitorEnabledFlag bool

// JawsMonitorEnabled reports whether JAWS PDU monitoring is enabled via the
// PCS_JAWS_MONITOR environment variable (default false).  It is consulted
// both when starting JawsMonitor and when deciding whether JAWS outlets are
// excluded from the normal Redfish power status sweep, so the two can never
// disagree and leave JAWS outlets unmonitored.
func JawsMonitorEnabled() bool {
	jawsMonitorEnabledOnce.Do(func() {
		envstr := os.Getenv("PCS_JAWS_MONITOR")
		if envstr == "" {
			return
		}
		yn, err := strconv.ParseBool(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_JAWS_MONITOR, defaulting to %t",
				jawsMonitorEnabledFlag)
			return
		}
		jawsMonitorEnabledFlag = yn
	})
	return jawsMonitorEnabledFlag
}

// Load JAWS outlets and store
func JawsLoad(xname string, FQDN string, authUser string, authPass string) {
	timeout := 20
	client := jawsHTTPClient()

	var req *retryablehttp.Request

	// jaws/monitor/outlets
	jurl, _ := url.Parse("https://" + FQDN + "/jaws/monitor/outlets")
	req, err := retryablehttp.NewRequest("GET", jurl.String(), nil)
	if err != nil {
		logger.Log.Error(err)
		return
	}

	reqContext, reqCtxCancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	req = req.WithContext(reqContext)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("cache-control", "no-cache")

	if !(authUser == "" && authPass == "") {
		req.SetBasicAuth(authUser, authPass)
	}

	resp, err := client.Do(req)
	defer drainAndCloseBodyWithCtxCancel(resp, reqCtxCancel)
	if err != nil {
		logger.Log.Error(err)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logger.Log.Errorf("JAWS: request for PDU %s returned HTTP status %s",
			xname, resp.Status)
		return
	}

	body, err := io.ReadAll(resp.Body)
	var eps []JawsEndpointStatus
	if err != nil {
		logger.Log.Error(err)
		return
	}
	err = json.Unmarshal(body, &eps)
	if err != nil {
		logger.Log.Error(err)
		return
	}

	// Store power state for each outlet
	for _, jep2 := range eps {
		epxname, ok := jaws2xname(xname, jep2.Id)
		if !ok {
			logger.Log.Errorf("JAWS: invalid outlet ID %q for PDU %s; skipping",
				jep2.Id, xname)
			continue
		}

		powerState := pcsmodel.PowerStateFilter_Undefined
		if strings.EqualFold(jep2.State, "on") {
			powerState = pcsmodel.PowerStateFilter_On
		} else if strings.EqualFold(jep2.State, "off") {
			powerState = pcsmodel.PowerStateFilter_Off
		}

		updateHWState(epxname, powerState, pcsmodel.ManagementStateFilter_available, "")
	}
}

// Convert JAWS outlet name to an xname
// example: x3000m0p0v17
func jaws2xname(xname string, id string) (string, bool) {
	if len(id) < 3 {
		return "", false
	}

	phase := id[0]
	if phase >= 'a' && phase <= 'z' {
		phase -= 'a' - 'A'
	}
	if phase < 'A' || phase > 'Z' {
		return "", false
	}
	for _, digit := range id[2:] {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}

	return xname + "p" + strconv.Itoa(int(phase)-int('A')) + "v" + id[2:], true
}

func isJawsURI(uri string) bool {
	parsedURI, err := url.Parse(uri)
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(parsedURI.Path, "/") {
		if segment == "jaws" {
			return true
		}
	}
	return false
}

// JAWS loop to monitor PDUs
func JawsMonitor(looptime int) {
	logger.Log.Info("In JAWS Monitor")
	if looptime <= 0 {
		looptime = 20
	}
	type jawsPDU struct {
		xname string
		fqdn  string
	}
	// Loop forever
	for {
		// Only the power status master polls PDUs; hwStateMap is only
		// populated on the master, so other replicas would just log
		// "no HW map entry" errors for every outlet.
		if !isPowerStatusMaster.Load() {
			time.Sleep(time.Duration(looptime) * time.Second)
			continue
		}
		pduList := []jawsPDU{}
		// Find PDUs
		compMap, err := hsmHandle.FillHSMData([]string{"all"})
		if err != nil {
			logger.Log.Error("JAWS FillHMSDATA ERROR: ", err)
		} else {
			for _, comp := range compMap {
				if comp.BaseData.Type == "CabinetPDUController" {
					if isJawsURI(comp.PowerStatusURI) {
						if comp.RfFQDN == "" {
							logger.Log.Errorf("JAWS: missing FQDN for PDU %s; skipping",
								comp.BaseData.ID)
							continue
						}
						pduList = append(pduList, jawsPDU{
							xname: comp.BaseData.ID,
							fqdn:  comp.RfFQDN,
						})
					}
				}
			}
		}
		for _, pdu := range pduList {
			logger.Log.Info("JAWS Load: " + pdu.xname)
			var user, pw string
			if GLOB.VaultEnabled {
				user, pw, err = GLOB.CS.GetControllerCredentials(pdu.xname)
				if err != nil {
					logger.Log.WithFields(logrus.Fields{"ERROR": err}).Error("Unable to get credentials for " + pdu.xname)
				}
			}
			JawsLoad(pdu.xname, pdu.fqdn, user, pw)
		}
		// Wait for a bit
		time.Sleep(time.Duration(looptime) * time.Second)
	}
}

func drainAndCloseBodyWithCtxCancel(resp *http.Response, ctxCancel context.CancelFunc) {
	// Must always drain and close response bodies
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body) // ok even if already drained
		resp.Body.Close()
	}
	// Call context cancel function, if supplied.  This must be done after
	// draining and closing the response body
	if ctxCancel != nil {
		ctxCancel()
	}
}
