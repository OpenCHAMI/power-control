package domain

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/openchami/power-control/v2/internal/hsm"
	"github.com/openchami/power-control/v2/internal/logger"
	"github.com/openchami/power-control/v2/internal/model"
	"github.com/openchami/power-control/v2/internal/storage"
)

type lookupStorage struct {
	storage.StorageProvider
	err error
}

func (s lookupStorage) GetTransition(uuid.UUID) (*model.Transition, *model.Transition, error) {
	return nil, nil, s.err
}
func (s lookupStorage) GetPowerCapTask(uuid.UUID) (*model.PowerCapTask, error) {
	return nil, s.err
}
func (s lookupStorage) GetPowerStatus(string) (*model.PowerStatusComponent, error) {
	return nil, s.err
}

func TestMissingEntitiesAndStorageErrors(t *testing.T) {
	previous, previousLogger := GLOB, logger.Log
	logger.Log = logrus.New()
	t.Cleanup(func() { GLOB, logger.Log = previous, previousLogger })
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"missing", nil, http.StatusNotFound},
		{"backend failure", errors.New("connection lost"), http.StatusInternalServerError},
		{"backend error containing old sentinel", errors.New("database does not exist"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			GLOB = &DOMAIN_GLOBALS{DSP: lookupStorage{err: tc.err}}
			id := uuid.New()
			for name, call := range map[string]func(uuid.UUID) model.Passback{
				"get transition":   GetTransition,
				"abort transition": AbortTransitionID,
				"get power cap":    GetPowerCapQuery,
			} {
				t.Run(name, func(t *testing.T) {
					response := call(id)
					require.True(t, response.IsError)
					require.Equal(t, tc.status, response.StatusCode)
				})
			}
			aborted, err := checkAbort(model.Transition{TransitionID: id})
			require.False(t, aborted)
			require.ErrorIs(t, err, tc.err)

			states, missing, err := getPowerStateHierarchy([]string{"x0c0s0b0n0"})
			require.ErrorIs(t, err, tc.err)
			require.Empty(t, states)
			if tc.err == nil {
				require.Equal(t, []string{"x0c0s0b0n0"}, missing)
			}

			// Missing records or failed reads must stop workers before generating tasks.
			require.NotPanics(t, func() { doTransition(id) })
			require.NotPanics(t, func() { doPowerCapTask(id) })

			supplies := getPowerSupplies(&hsm.HsmData{PoweredBy: []string{"x0m0p0v1"}})
			require.Len(t, supplies, 1)
			require.Equal(t, model.PowerStateFilter_Undefined, supplies[0].State)

			if tc.err != nil {
				// A storage failure must not turn into a write of a supposedly new record.
				_, err := storeTransition(model.Transition{TransitionID: id})
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestStoreMissingTransition(t *testing.T) {
	previous, previousLogger := GLOB, logger.Log
	logger.Log = logrus.New()
	t.Cleanup(func() { GLOB, logger.Log = previous, previousLogger })
	sp := &storage.MEMStorage{}
	require.NoError(t, sp.Init(nil))
	t.Cleanup(func() { require.NoError(t, sp.Close()) })
	GLOB = &DOMAIN_GLOBALS{DSP: sp}
	tr := model.Transition{TransitionID: uuid.New(), Status: model.TransitionStatusNew}
	aborted, err := storeTransition(tr)
	require.NoError(t, err)
	require.False(t, aborted)
	got, _, err := sp.GetTransition(tr.TransitionID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, tr.TransitionID, got.TransitionID)
	require.Equal(t, tr.Status, got.Status)
}
