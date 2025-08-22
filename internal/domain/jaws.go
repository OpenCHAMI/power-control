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
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"

	"github.com/OpenCHAMI/power-control/v2/internal/logger"
	pcsmodel "github.com/OpenCHAMI/power-control/v2/internal/model"
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

// Load JAWS outlets and store
func JawsLoad(xname string, FQDN string, authUser string, authPass string) {
	timeout := 20
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := retryablehttp.NewClient()
	client.HTTPClient.Transport = transport

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
		epxname := jaws2xname(xname, jep2.Id)

		powerState := pcsmodel.PowerStateFilter_Undefined
		if strings.EqualFold(jep2.State, "on") {
			powerState = pcsmodel.PowerStateFilter_On
		} else if strings.EqualFold(jep2.State, "off") {
			powerState = pcsmodel.PowerStateFilter_Off
		}

		updateHWState(epxname, powerState, pcsmodel.ManagementStateFilter_available, "")
	}
	return
}

// Convert JAWS outlet name to an xname
// example: x3000m0p0v17
func jaws2xname(xname string, id string) string {
	nxname := xname
	nxname = nxname + "p" + strconv.Itoa(int(id[0])-int('A'))
	nxname = nxname + "v" + id[2:]
	return nxname
}

// JAWS loop to monitor PDUs
func JawsMonitor(looptime int) {
	logger.Log.Info("In JAWS Monitor")
	if looptime == 0 {
		looptime = 20
	}
	// Loop forever
	for {
		xnameList := []string{}
		// Find PDUs
		compMap, err := (*hsmHandle).FillHSMData([]string{"all"})
		if err != nil {
			logger.Log.Error("JAWS FillHMSDATA ERROR: ", err)
		} else {
			for _, xname := range compMap {
				if xname.BaseData.Type == "CabinetPDUController" {
					if xname.PowerStatusURI == "/jaws" {
						xnameList = append(xnameList, xname.BaseData.ID)
					}
				}
			}
		}
		for _, xname := range xnameList {
			logger.Log.Info("JAWS Load: " + xname)
			var user, pw string
			if GLOB.VaultEnabled {
				user, pw, err = (*GLOB.CS).GetControllerCredentials(xname)
				if err != nil {
					logger.Log.WithFields(logrus.Fields{"ERROR": err}).Error("Unable to get credentials for " + xname)
				}
			}
			JawsLoad(xname, xname, user, pw)
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
