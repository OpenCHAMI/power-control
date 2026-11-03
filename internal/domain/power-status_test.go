//go:build !integration_tests

// MIT License
//
// (C) Copyright [2022-2024,2025] Hewlett Packard Enterprise Development LP
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
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	base "github.com/Cray-HPE/hms-base/v2"
	"github.com/Cray-HPE/hms-certs/pkg/hms_certs"
	trsapi "github.com/Cray-HPE/hms-trs-app-api/v3/pkg/trs_http_api"
	"github.com/Cray-HPE/hms-xname/xnametypes"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/openchami/power-control/v2/internal/credstore"
	"github.com/openchami/power-control/v2/internal/hsm"
	pcsmodel "github.com/openchami/power-control/v2/internal/model"
	"github.com/openchami/power-control/v2/internal/storage"
)

type PwrStat_TS struct {
	suite.Suite
}

var tlogger = logrus.New()
var domGlb DOMAIN_GLOBALS
var domGlbInit = false
var smURL string
var running = true

func glbInit() {
	if !domGlbInit {
		var credStoreGlob credstore.CREDSTORE_GLOBALS
		lsvcName := "PwrControlTest"

		var TLOC_rf trsapi.TrsAPI
		var CS credstore.CredStoreProvider
		tcs := &credstore.VAULTv0{}
		var HSM hsm.HSMProvider
		HSM = &hsm.HSMv2{}
		var DSP storage.StorageProvider
		sprov := &storage.MEMStorage{Logger: tlogger}
		var DLOCK storage.DistributedLockProvider
		tdLock := &storage.MEMLockProvider{Logger: tlogger}

		vaultEnabled := false
		if os.Getenv("VAULT_ENABLED") != "" {
			vaultEnabled = true
		}

		if vaultEnabled {
			CS = tcs
			vkp := os.Getenv("VAULT_KEYPATH")
			if vkp == "" {
				vkp = "hms-creds" //sane default
			}
			credStoreGlob.NewGlobals(tlogger, &running, 3600, vkp)
			CS.Init(&credStoreGlob)
		}

		svcClient, _ := hms_certs.CreateRetryableHTTPClientPair("", 10, 5, 5)
		hsmGlob := hsm.HSM_GLOBALS{
			SvcName:       lsvcName,
			Logger:        tlogger,
			Running:       &running,
			LockEnabled:   true,
			SMUrl:         os.Getenv("SMS_SERVER"),
			SVCHttpClient: svcClient,
		}
		HSM.Init(&hsmGlob)
		smURL = hsmGlob.SMUrl

		DSP = sprov
		DSP.Init(tlogger)

		winsec := &trsapi.TRSHTTPLocal{}
		winsec.Logger = tlogger
		TLOC_rf = winsec
		TLOC_rf.Init(lsvcName, tlogger)

		DLOCK = tdLock
		DLOCK.Init(tlogger)

		domGlb.NewGlobals(nil, TLOC_rf, nil, nil, svcClient, &sync.RWMutex{},
			&running, DSP, HSM, vaultEnabled, CS, DLOCK, 20000, 1440,
			"power-status_test-pod")

		tlogger.Errorf("DLOCK: '%v', Globals: '%v'", DLOCK, domGlb)
		domGlbInit = true
	}
}

type bAuth struct {
	username string
	password string
}

//Convenience func to do HTTP requests to prevent code duplication.

func doHTTP(url string, method string, pld []byte, auth *bAuth) ([]byte, int, error) {
	var rdata []byte
	var req *http.Request

	svcClient, err := hms_certs.CreateRetryableHTTPClientPair("", 10, 10, 4)
	if err != nil {
		return rdata, 0, fmt.Errorf("ERROR creating retryable client pair: %v", err)
	}

	if method == http.MethodGet {
		req, err = http.NewRequest(method, url, nil)
	} else {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(pld))
	}
	if err != nil {
		return rdata, 0, fmt.Errorf("Error creating HTTP request: %v", err)
	}

	if auth != nil {
		req.SetBasicAuth(auth.username, auth.password)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")

	rsp, perr := svcClient.Do(req)
	defer base.DrainAndCloseResponseBody(rsp)

	if perr != nil {
		return rdata, 0, fmt.Errorf("Error performing http %s: %v", method, perr)
	}

	rdata, err = io.ReadAll(rsp.Body)

	if err != nil {
		return rdata, 0, fmt.Errorf("Error reading http rsp body: %v", err)
	}

	return rdata, rsp.StatusCode, nil
}

//Remove a component from HSM.  This is used to test a specific "map sync"
//function in the power status code.

func removeComponent(xname string) error {
	smURL := os.Getenv("SMS_SERVER")
	if smURL == "" {
		return fmt.Errorf("INFO: Can't get SM URL from env, nothing to do.")
	}

	_, scode, err := doHTTP(smURL+"/hsm/v2/State/Components"+"/"+xname,
		http.MethodDelete, nil, nil)
	if err != nil {
		return fmt.Errorf("Error in HTTP DELETE request for HSM components: %v",
			err)
	}
	if scode != http.StatusOK {
		return fmt.Errorf("Error deleting HSM component, bad rsp code: %d", scode)
	}

	return nil
}

func rediscoverNode(xname string) error {
	smURL := os.Getenv("SMS_SERVER")
	if smURL == "" {
		return fmt.Errorf("INFO: Can't get SM URL from env, nothing to do.")
	}

	bmc := xnametypes.GetHMSCompParent(xname)

	pld := []byte(fmt.Sprintf("{\"xnames\":[\"%s\"]}", bmc))

	_, scode, err := doHTTP(smURL+"/hsm/v2/Inventory/Discover", http.MethodPost, pld, nil)
	if err != nil {
		return fmt.Errorf("Error in HTTP POST request for HSM discover: %v",
			err)
	}
	if scode != http.StatusOK {
		return fmt.Errorf("Error rediscovering HSM component, bad rsp code: %d", scode)
	}

	return nil
}

func turnNodeOff(node *hsm.HsmData) error {
	return nodePower(node, "Off")
}

func turnNodeOn(node *hsm.HsmData) error {
	return nodePower(node, "On")
}

func nodePower(node *hsm.HsmData, action string) error {
	var auth bAuth

	//Get the creds from vault

	if !vaultEnabled {
		return fmt.Errorf("Vault not enabled, can't get creds for BMC.")
	}

	un, pw, cerr := domGlb.CS.GetControllerCredentials(node.BaseData.ID)
	if cerr != nil {
		return fmt.Errorf("Can't get creds for %s: %v", node.BaseData.ID, cerr)
	}

	// Some nodes support GracefulShutdown some support Off
	resetType := action
	for _, action := range node.AllowableActions {
		if action == "GracefulShutdown" {
			resetType = action
		}
	}

	url := "https://" + node.RfFQDN + node.PowerActionURI
	pld := []byte(fmt.Sprintf("{\"ResetType\":\"%s\"}", resetType))
	auth.username = un
	auth.password = pw

	_, scode, err := doHTTP(url, http.MethodPost, pld, &auth)
	if err != nil {
		return fmt.Errorf("ERROR in doHTTP: %v", err)
	}
	if (scode != http.StatusOK) && (scode != http.StatusNoContent) {
		return fmt.Errorf("Node off returned bad status code: %d", scode)
	}

	return nil
}

func printCompList(t *testing.T, hdr string, rcomp pcsmodel.PowerStatus) {
	delim := "==========================================="
	spc := "    "

	t.Logf("%s", delim)
	t.Logf("%s %s", spc, hdr)

	for _, comp := range rcomp.Status {
		t.Logf("%s", delim)
		t.Logf("%s '%s'", spc, comp.XName)
		t.Logf("%s PowerState: '%s'", spc, comp.PowerState)
		t.Logf("%s ManagementState: '%s'", spc, comp.ManagementState)
		t.Logf("%s Error: '%s'", spc, comp.Error)
		t.Logf("%s SuppPwrTrans: '%v'", spc, comp.SupportedPowerTransitions)
		t.Logf("%s LastUpdated: '%s'", spc, comp.LastUpdated)
	}
}

func (suite *PwrStat_TS) Test_PowerStatusMonitor() {
	var t *testing.T

	t = suite.T()

	//First initialize the power monitor
	err := PowerStatusMonitorInit(&domGlb, (600 * time.Second), tlogger, (5 * time.Second), 30, 3, 100, 2)
	suite.Assert().Equal(nil, err, "PowerStatusMonitorInit() error: %v", err)

	//Wait a while for the internal componant map to get updated.

	time.Sleep(10 * time.Second)

	//Get a list of components from HSM

	compMap, cerr := domGlb.HSM.FillHSMData([]string{"all"})
	suite.Assert().Equal(nil, cerr, "FillHSMData() failed: %v", cerr)

	var comps []string
	for k := range compMap {
		comps = append(comps, k)
	}

	//Get the power states of each component

	pb := GetPowerStatus(comps, pcsmodel.PowerStateFilter_Nil,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")

	//Print them out

	var rcomp pcsmodel.PowerStatus
	rcomp = pb.Obj.(pcsmodel.PowerStatus)

	suite.Assert().NotEqual(0, len(rcomp.Status),
		"GetPowerStatus() failed, has 0 components.")

	printCompList(t, "ALL COMPONENTS", rcomp)

	//Filter out unsupported comptypes.
	comps = []string{}
	for _, comp := range rcomp.Status {
		if len(comp.Error) == 0 {
			comps = append(comps, comp.XName)
		}
	}

	//Ask for Off nodes.  There should be none.

	pb = GetPowerStatus(comps, pcsmodel.PowerStateFilter_Off,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")
	rcomp = pb.Obj.(pcsmodel.PowerStatus)

	suite.Assert().Equal(0, len(rcomp.Status),
		"GetPowerStatus() failed, should have 0 components.")

	//Turn a node off, then get power status again, then get power
	//status filtering on the On components and the Off component, verifying
	//correctness.

	t.Logf("Testing GetPowerStatus(), filtering for OFF nodes.")
	var offNode string
	var onNodes []string

	for _, v := range compMap {
		if v.BaseData.Type == string(xnametypes.Node) {
			offNode = v.BaseData.ID
			break
		}
	}
	if offNode == "" {
		t.Logf("WARNING: No 'Off' node components found in component map!  Can't test power state filtering.")
		return
	}

	//Turn a node off.

	t.Logf("Turning node %s off.", offNode)
	err = turnNodeOff(compMap[offNode])
	suite.Assert().Equal(nil, err, "Error turning off node '%s': %v", offNode, err)

	//Wait for the monitor to catch up
	time.Sleep(10 * time.Second)

	pb = GetPowerStatus([]string{offNode}, pcsmodel.PowerStateFilter_Off,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")

	rcomp = pb.Obj.(pcsmodel.PowerStatus)

	//Should be exactly one component.

	suite.Assert().Equal(1, len(rcomp.Status),
		"GetPowerStatus() failed, has %d components (expecting 1).",
		len(rcomp.Status))

	if len(rcomp.Status) > 0 {
		//Need exact match of "off" component

		suite.Assert().Equal(offNode, rcomp.Status[0].XName,
			"Mismatch of 'off' node, exp: '%s', got: '%s'",
			offNode, rcomp.Status[0].XName)

		printCompList(t, "OFF COMPONENTS", rcomp)
	}

	//Now do the same thing, matching the remaining ON nodes

	t.Logf("Testing GetPowerStatus(), filtering for ON nodes.")

	for _, v := range compMap {
		if v.BaseData.Type == string(xnametypes.Node) {
			onNodes = append(onNodes, v.BaseData.ID)
		}
	}
	if len(onNodes) == 0 {
		t.Logf("WARNING: No 'On' node components found in component map!  Can't test power state filtering.")
		return
	}

	pb = GetPowerStatus(onNodes, pcsmodel.PowerStateFilter_On,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")

	rcomp = pb.Obj.(pcsmodel.PowerStatus)

	//Number of components must match.  We turned one node off, so subtract
	//that from the total node count.

	expOnCount := len(onNodes) - 1

	suite.Assert().Equal(expOnCount, len(rcomp.Status),
		"GetPowerStatus() failed, has %d components (expecting %d).",
		len(rcomp.Status), expOnCount)

	if len(rcomp.Status) > 0 {
		//Need exact match of each "on" component

		nmatches := 0
		for _, onn := range onNodes {
			for _, snn := range rcomp.Status {
				if onn == snn.XName {
					nmatches++
					break
				}
			}
		}

		suite.Assert().Equal(expOnCount, nmatches,
			"Mismatch number of 'on' nodes, exp: '%s', got: '%s'",
			expOnCount, nmatches)

		printCompList(t, "ON NODES", rcomp)
	}

	//Check GetPowerStatus() with an invalid xname

	t.Logf("Testing GetPowerStatus(), filtering for ERROR nodes.")

	enodes := []string{"x1234c7s7b0n3", "x1000c0s0e0", "xyzzy"}
	pb = GetPowerStatus(enodes, pcsmodel.PowerStateFilter_Nil,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")

	rcomp = pb.Obj.(pcsmodel.PowerStatus)

	suite.Assert().Equal(len(enodes), len(rcomp.Status),
		"GetPowerStatus() failed, has %d components (expecting %d).",
		len(rcomp.Status), len(enodes))

	if len(rcomp.Status) > 0 {
		//Make sure they all have an error message

		for _, cmp := range rcomp.Status {
			suite.Assert().NotEqual("", cmp.Error,
				"GetPowerStatus() with unhandled xname/type should have failed, did not.")
		}

		printCompList(t, "ERROR COMPONENTS", rcomp)
	}

	//Delete a component; wait for mapper to catch up; query all components'
	//power state; verify that the deleted one is not there.

	t.Logf("Deleting node %s from HSM", offNode)
	err = removeComponent(offNode)
	suite.Assert().Equal(nil, err, "Error removing node '%s': %v", offNode, err)

	//Wait for the monitor to catch up
	time.Sleep(10 * time.Second)

	pb = GetPowerStatus(comps, pcsmodel.PowerStateFilter_Nil,
		pcsmodel.ManagementStateFilter_Nil)

	suite.Assert().Equal(http.StatusOK, pb.StatusCode,
		"GetPowerStatus() failed with bad status code: %d", pb.StatusCode)
	suite.Assert().False(pb.IsError, "GetPowerStatus() failed, IsError is true.")

	rcomp = pb.Obj.(pcsmodel.PowerStatus)
	printCompList(t, "ALL COMPONENTS MINUS ONE", rcomp)

	// Clean up
	PowerStatusMonitorStop()
	turnNodeOn(compMap[offNode])
	time.Sleep(10 * time.Second)
	rediscoverNode(offNode)
	time.Sleep(5 * time.Second)
}

func Test_PowerStatusStuff(t *testing.T) {
	glbInit()
	suite.Run(t, new(PwrStat_TS))
}

const refreshStatePath = "/hsm/v2/State/Components/Query"
const refreshEndpointPath = "/hsm/v2/Inventory/ComponentEndpoints"
const refreshComponents = `{"Components":[{"ID":"x0c0s0b0n0","Type":"Node","State":"Ready"},{"ID":"x0c0s1b0n0","Type":"Node","State":"Ready"}]}`

type refreshTransport func(*http.Request) (*http.Response, error)

func (f refreshTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func successfulRefreshBody(t *testing.T, req *http.Request) string {
	t.Helper()
	if req.URL.Path == refreshStatePath {
		require.Equal(t, http.MethodPost, req.Method)
		return refreshComponents
	}
	require.Equal(t, refreshEndpointPath, req.URL.Path)
	require.Equal(t, http.MethodGet, req.Method)
	var endpoints []string
	for _, id := range req.URL.Query()["id"] {
		endpoints = append(endpoints, fmt.Sprintf(`{"ID":%q,"ComponentEndpointType":"ComponentEndpointComputerSystem","RedfishEndpointFQDN":"new-bmc","OdataID":"/redfish/v1/Systems/1"}`, id))
	}
	return `{"ComponentEndpoints":[` + strings.Join(endpoints, ",") + `]}`
}

func setupComponentRefresh(t *testing.T, transport refreshTransport) *hsm.HSMv2 {
	t.Helper()
	previousHSM, previousMap, previousStore, previousLogger := hsmHandle, hwStateMap, kvStore, glogger
	t.Cleanup(func() {
		hsmHandle, hwStateMap, kvStore, glogger = previousHSM, previousMap, previousStore, previousLogger
	})
	glogger = logrus.New()
	store := &storage.MEMStorage{}
	require.NoError(t, store.Init(glogger))
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	kvStore = store
	hwStateMap = make(map[string]*componentPowerInfo)
	for _, id := range []string{"x0c0s0b0n0", "x0c0s1b0n0"} {
		info := &componentPowerInfo{
			PSComp:  pcsmodel.PowerStatusComponent{XName: id, PowerState: "on"},
			HSMData: hsm.HsmData{RfFQDN: "previous-bmc", PowerStatusURI: "/previous/status", PowerActionURI: "/previous/reset"},
		}
		hwStateMap[id] = info
		require.NoError(t, store.StorePowerStatus(info.PSComp))
	}
	client, err := hms_certs.CreateRetryableHTTPClientPair("", 1, 0, 1)
	require.NoError(t, err)
	client.SecureClient.HTTPClient.Transport = transport
	t.Cleanup(client.CloseIdleConnections)
	handle := &hsm.HSMv2{}
	require.NoError(t, handle.Init(&hsm.HSM_GLOBALS{SMUrl: "http://smd", SVCHttpClient: client, Logger: glogger}))
	hsmHandle = handle
	return handle
}

func TestUpdateComponentMapPreservesSnapshotOnFailure(t *testing.T) {
	testCases := []struct {
		name       string
		path       string
		status     int
		body       string
		laterBatch bool
	}{
		{name: "state unauthorized with components", path: refreshStatePath, status: http.StatusUnauthorized, body: refreshComponents},
		{name: "state partial content", path: refreshStatePath, status: http.StatusPartialContent, body: refreshComponents},
		{name: "state missing components", path: refreshStatePath, status: http.StatusOK, body: `{}`},
		{name: "state null response", path: refreshStatePath, status: http.StatusOK, body: `null`},
		{name: "state null components", path: refreshStatePath, status: http.StatusOK, body: `{"Components":null}`},
		{name: "endpoint unauthorized", path: refreshEndpointPath, status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`},
		{name: "endpoint partial content", path: refreshEndpointPath, status: http.StatusPartialContent, body: `{"ComponentEndpoints":[]}`},
		{name: "endpoint unavailable", path: refreshEndpointPath, status: http.StatusServiceUnavailable, body: `{}`},
		{name: "endpoint missing inventory", path: refreshEndpointPath, status: http.StatusOK, body: `{}`},
		{name: "endpoint null response", path: refreshEndpointPath, status: http.StatusOK, body: `null`},
		{name: "endpoint null inventory", path: refreshEndpointPath, status: http.StatusOK, body: `{"ComponentEndpoints":null}`},
		{name: "endpoint truncated inventory", path: refreshEndpointPath, status: http.StatusOK, body: `{"ComponentEndpoints":[`},
		{name: "later batch failure", path: refreshEndpointPath, status: http.StatusOK, body: `{}`, laterBatch: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			endpointCalls := 0
			handle := setupComponentRefresh(t, func(req *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, successfulRefreshBody(t, req)
				if req.URL.Path == refreshEndpointPath {
					endpointCalls++
				}
				if req.URL.Path == testCase.path && (!testCase.laterBatch || endpointCalls == 2) {
					status, body = testCase.status, testCase.body
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			if testCase.laterBatch {
				handle.HSMGlobals.MaxComponentQuery = 1
			}
			before := make(map[string]componentPowerInfo)
			for id, info := range hwStateMap {
				before[id] = *info
			}

			require.Error(t, updateComponentMap())
			require.Len(t, hwStateMap, len(before))
			for id, info := range before {
				require.Equal(t, &info, hwStateMap[id])
				stored, err := kvStore.GetPowerStatus(id)
				require.NoError(t, err)
				require.Equal(t, &info.PSComp, stored)
			}
		})
	}
}

func TestUpdateComponentMapSuccessfulRefresh(t *testing.T) {
	requests := 0
	emptyEndpoints := false
	emptyComponents := false
	handle := setupComponentRefresh(t, func(req *http.Request) (*http.Response, error) {
		requests++
		body := successfulRefreshBody(t, req)
		if emptyEndpoints && req.URL.Path == refreshEndpointPath {
			body = `{"ComponentEndpoints":[]}`
		}
		if emptyComponents && req.URL.Path == refreshStatePath {
			body = `{"Components":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	require.NoError(t, updateComponentMap())
	require.Len(t, hwStateMap, 2)
	for _, info := range hwStateMap {
		require.Equal(t, "new-bmc", info.HSMData.RfFQDN)
		require.Equal(t, "/redfish/v1/Systems/1", info.HSMData.PowerStatusURI)
		require.Equal(t, "on", info.PSComp.PowerState)
	}
	require.Equal(t, 2, requests, "refresh should use one state query and one bulk endpoint request")

	emptyEndpoints = true
	data, err := handle.FillHSMData([]string{"all"})
	require.NoError(t, err, "valid empty endpoint arrays must remain supported")
	require.Len(t, data, 2)
	require.Equal(t, 4, requests)

	emptyComponents = true
	data, err = handle.FillHSMData([]string{"all"})
	require.NoError(t, err, "valid empty component arrays must remain supported")
	require.Empty(t, data)
	require.Equal(t, 5, requests, "empty component lists should not query endpoints")
}
