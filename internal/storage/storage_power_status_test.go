package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
)

func TestGetPowerStatusMaster(t *testing.T) {
	// We make the assumption that we are dealing a clean DB here, we may not be!
	_, err := storageProvider.GetPowerStatusMaster()
	require.Error(t, err)

	// Ensure that the contains the magic string that indicates the power status master does not exist!
	// This what is expected from the ETCD implementaton this should be reworked!
	require.Contains(t, err.Error(), "does not exist")

}

func TestStorePowerStatusMaster(t *testing.T) {
	now := time.Now().Truncate(time.Microsecond)

	err := storageProvider.StorePowerStatusMaster(now)
	require.NoError(t, err)

	lastUpdated, err := storageProvider.GetPowerStatusMaster()
	require.NoError(t, err)
	require.WithinDuration(t, now, lastUpdated, time.Microsecond, "Stored power status master timestamp does not match the retrieved timestamp")
}

func TestTASPowerStatusMaster(t *testing.T) {
	now := time.Now()
	newVal := now.Add(10 * time.Second)

	// Store the power status master first
	err := storageProvider.StorePowerStatusMaster(now)
	require.NoError(t, err)

	result, err := storageProvider.TASPowerStatusMaster(newVal, now)
	require.NoError(t, err)
	require.True(t, result, "TASPowerStatusMaster should return true")

	// Now test with a value that is equal to the stored value
	lastUpdated, err := storageProvider.GetPowerStatusMaster()
	require.NoError(t, err)
	require.WithinDuration(t, newVal, lastUpdated, time.Microsecond, "Stored power status master timestamp does not match the retrieved timestamp")

	// Now test with a value is differen from the stored value
	result, err = storageProvider.TASPowerStatusMaster(now, time.Now())
	require.NoError(t, err)
	require.False(t, result, "TASPowerStatusMaster should return false when the test value is less than the stored value")

	// Now test the value has not changed
	lastUpdated, err = storageProvider.GetPowerStatusMaster()
	require.NoError(t, err)
	require.WithinDuration(t, newVal, lastUpdated, time.Microsecond, "Stored power status master timestamp does not match the retrieved timestamp")
}

func TestStorePowerStatus(t *testing.T) {
	ps := model.PowerStatusComponent{
		XName:                     "x0c0s0b0n0",
		PowerState:                "on",
		ManagementState:           "available",
		SupportedPowerTransitions: []string{"On", "Off"},
		Error:                     "OK",
	}

	err := storageProvider.StorePowerStatus(ps)
	require.NoError(t, err)

	retrievedPS, err := storageProvider.GetPowerStatus(ps.XName)
	require.NoError(t, err)
	require.Equal(t, ps.XName, retrievedPS.XName, "XName should match")
	require.Equal(t, ps.PowerState, retrievedPS.PowerState, "PowerState should match")
	require.Equal(t, ps.ManagementState, retrievedPS.ManagementState, "ManagementState should match")
	require.Equal(t, ps.SupportedPowerTransitions, retrievedPS.SupportedPowerTransitions, "SupportedPowerTransitions should match")
	require.Equal(t, ps.Error, retrievedPS.Error, "Error should match")

	// Now try with LastUpdated set
	ps.LastUpdated = time.Now().Truncate(time.Microsecond)
	err = storageProvider.StorePowerStatus(ps)
	require.NoError(t, err)

	retrievedPS, err = storageProvider.GetPowerStatus(ps.XName)
	require.NoError(t, err)
	require.True(t, ps.LastUpdated.Equal(retrievedPS.LastUpdated), "LastUpdated should match")
}

func TestDeletePowerStatus(t *testing.T) {
	ps := model.PowerStatusComponent{
		XName:                     "x0c0s0b0n0",
		PowerState:                "on",
		ManagementState:           "available",
		SupportedPowerTransitions: []string{"On", "Off", "Reboot"},
		LastUpdated:               time.Now().Truncate(time.Microsecond),
	}

	err := storageProvider.StorePowerStatus(ps)
	require.NoError(t, err)

	err = storageProvider.DeletePowerStatus(ps.XName)
	require.NoError(t, err)

	_, err = storageProvider.GetPowerStatus(ps.XName)
	require.Error(t, err, "Expected error when retrieving deleted power status")
}

func TestGetPowerStatusAll(t *testing.T) {
	ps := model.PowerStatusComponent{
		XName:                     "x0c0s0b0n1",
		PowerState:                "on",
		ManagementState:           "available",
		SupportedPowerTransitions: []string{"on", "off", "reboot"},
		LastUpdated:               time.Now(),
	}

	// Store multiple power status components with different XNames
	maxIX := 3

	for ix := 0; ix <= maxIX; ix++ {
		pst := ps
		pst.XName = fmt.Sprintf("x%dc%ds%db%dn%d", ix, ix, ix, ix, ix)
		err := storageProvider.StorePowerStatus(pst)
		require.NoError(t, err, "StorePowerStatus() failed for %s", pst.XName)
	}

	// Retrieve all power status components
	parr, paerr := storageProvider.GetAllPowerStatus()
	require.NoError(t, paerr, "GetAllPowerStatus() failed")

	paMap := make(map[string]*model.PowerStatusComponent)
	for ix := 0; ix < len(parr.Status); ix++ {
		t.Logf("Fetched power status element[%d]: '%v'", ix, parr.Status[ix])
		paMap[parr.Status[ix].XName] = &parr.Status[ix]
	}

	for ix := 0; ix <= maxIX; ix++ {
		xn := fmt.Sprintf("x%dc%ds%db%dn%d", ix, ix, ix, ix, ix)
		require.Equal(t, paMap[xn].XName, xn, "GetAllPowerStatus() array mismatch, exp: '%s', got: '%s'",
			xn, paMap[xn].XName)
	}
}

func TestGetPowerStatusHierarchy(t *testing.T) {
	ps := model.PowerStatusComponent{
		XName:                     "x0c0s0b0n1",
		PowerState:                "on",
		ManagementState:           "available",
		SupportedPowerTransitions: []string{"on", "off", "reboot"},
		LastUpdated:               time.Now().Truncate(time.Microsecond),
	}

	numberOfComponents := 5

	xnamePrefix := "x4c1s1b1"

	xnames := make([]string, numberOfComponents)
	for i := 0; i < numberOfComponents; i++ {
		xnames[i] = fmt.Sprintf("%s%dn%d", xnamePrefix, i, i)
	}

	// Store multiple power status components with different XNames, with a common prefix
	for i := 0; i < numberOfComponents; i++ {
		pst := ps
		pst.XName = xnames[i]
		err := storageProvider.StorePowerStatus(pst)
		require.NoError(t, err, "StorePowerStatus() failed for %s", pst.XName)
	}

	// Retrieve with hierarchy
	powerStatus, paerr := storageProvider.GetPowerStatusHierarchy(xnamePrefix)
	require.NoError(t, paerr, "GetPowerStatusHierarchy() failed")
	require.Equal(t, numberOfComponents, len(powerStatus.Status), "GetPowerStatusHierarchy() should return %d components, got %d",
		numberOfComponents, len(powerStatus.Status))

	// Verify that all stored components are present in the hierarchy
	for i := 0; i < numberOfComponents; i++ {
		require.Contains(t, xnames, powerStatus.Status[i].XName, "XName '%s' not found in hierarchy", powerStatus.Status[i].XName)
	}
}

func TestPowerStatusInvalidXName(t *testing.T) {
	pErrComp := model.PowerStatusComponent{
		XName: "xyzzy",
	}

	err := storageProvider.StorePowerStatus(pErrComp)
	require.Error(t, err, "StorePowerStatus() with bad XName should have failed, did not.")

	err = storageProvider.DeletePowerStatus(pErrComp.XName)
	require.Error(t, err, "DeletePowerStatus() with bad XName should have failed, did not.")

	_, err = storageProvider.GetPowerStatus(pErrComp.XName)
	require.Error(t, err, "GetPowerStatus() with bad XName should have failed, did not.")
}
