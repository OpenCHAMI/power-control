package storage

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	hmetcd "github.com/Cray-HPE/hms-hmetcd"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/openchami/power-control/v2/internal/model"
)

func entityLookups(t *testing.T, sp StorageProvider) map[string]func() (any, error) {
	t.Helper()
	id := uuid.New()
	return map[string]func() (any, error){
		"master":              func() (any, error) { return sp.GetPowerStatusMaster() },
		"power status":        func() (any, error) { return sp.GetPowerStatus("x9999c7s7b7n7") },
		"power cap task":      func() (any, error) { return sp.GetPowerCapTask(id) },
		"power cap operation": func() (any, error) { return sp.GetPowerCapOperation(id, id) },
		"transition": func() (any, error) {
			tr, firstPage, err := sp.GetTransition(id)
			if tr == nil {
				require.Nil(t, firstPage)
			}
			return tr, err
		},
		"transition task": func() (any, error) { return sp.GetTransitionTask(id, id) },
	}
}

func testMissingEntities(t *testing.T, sp StorageProvider, includeMaster bool) {
	t.Helper()
	for name, lookup := range entityLookups(t, sp) {
		if name == "master" && !includeMaster {
			continue
		}
		t.Run(name, func(t *testing.T) {
			entity, err := lookup()
			require.NoError(t, err)
			require.Nil(t, entity)
		})
	}
}

func TestMemoryEntityLookups(t *testing.T) {
	sp := &MEMStorage{}
	require.NoError(t, sp.Init(nil))
	t.Cleanup(func() { require.NoError(t, sp.Close()) })
	testMissingEntities(t, sp, true)

	// Memory providers share a process-wide store, so clean up.
	t.Cleanup(func() {
		require.NoError(t, toETCDStorage(sp).kvDelete(keySegPowerStatusMaster))
	})
	now := time.Now().UTC()
	require.NoError(t, sp.StorePowerStatusMaster(now))
	master, err := sp.GetPowerStatusMaster()
	require.NoError(t, err)
	require.Equal(t, &now, master)

	status := model.PowerStatusComponent{XName: "x9999c7s7b7n7", PowerState: "on"}
	require.NoError(t, sp.StorePowerStatus(status))
	gotStatus, err := sp.GetPowerStatus(status.XName)
	require.NoError(t, err)
	require.Equal(t, &status, gotStatus)
	require.NoError(t, sp.DeletePowerStatus(status.XName))

	task := model.NewPowerCapSnapshotTask(model.PowerCapSnapshotParameter{}, 20)
	require.NoError(t, sp.StorePowerCapTask(task))
	gotTask, err := sp.GetPowerCapTask(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, gotTask)
	require.Equal(t, task.TaskID, gotTask.TaskID)
	require.NoError(t, sp.DeletePowerCapTask(task.TaskID))
	gotTask, err = sp.GetPowerCapTask(task.TaskID)
	require.NoError(t, err)
	require.Nil(t, gotTask)

	op := model.NewPowerCapOperation(task.TaskID, "snapshot")
	require.NoError(t, sp.StorePowerCapOperation(op))
	gotOp, err := sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	require.NoError(t, err)
	require.Equal(t, &op, gotOp)
	require.NoError(t, sp.DeletePowerCapOperation(task.TaskID, op.OperationID))
	gotOp, err = sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	require.NoError(t, err)
	require.Nil(t, gotOp)

	tr := model.Transition{TransitionID: uuid.New(), Status: model.TransitionStatusNew}
	require.NoError(t, sp.StoreTransition(tr))
	gotTr, firstPage, err := sp.GetTransition(tr.TransitionID)
	require.NoError(t, err)
	require.Equal(t, &tr, gotTr)
	require.Equal(t, gotTr, firstPage)
	gotTr.Status = model.TransitionStatusAbortSignaled
	require.Equal(t, model.TransitionStatusNew, firstPage.Status)
	changed, err := sp.TASTransition(*gotTr, *firstPage)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, sp.DeleteTransition(tr.TransitionID))
	gotTr, firstPage, err = sp.GetTransition(tr.TransitionID)
	require.NoError(t, err)
	require.Nil(t, gotTr)
	require.Nil(t, firstPage)

	trTask := model.TransitionTask{TransitionID: tr.TransitionID, TaskID: uuid.New()}
	require.NoError(t, sp.StoreTransitionTask(trTask))
	gotTrTask, err := sp.GetTransitionTask(tr.TransitionID, trTask.TaskID)
	require.NoError(t, err)
	require.Equal(t, &trTask, gotTrTask)
	require.NoError(t, sp.DeleteTransitionTask(tr.TransitionID, trTask.TaskID))
	gotTrTask, err = sp.GetTransitionTask(tr.TransitionID, trTask.TaskID)
	require.NoError(t, err)
	require.Nil(t, gotTrTask)
}

type lookupKV struct {
	hmetcd.Kvi
	value  string
	exists bool
	err    error
}

func (kv lookupKV) Get(string) (string, bool, error) { return kv.value, kv.exists, kv.err }

func TestETCDEntityLookupErrors(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	for _, tc := range []struct {
		name string
		kv   lookupKV
	}{
		{"backend error", lookupKV{err: backendErr}},
		{"backend error with value", lookupKV{value: "{}", exists: true, err: backendErr}},
		{"malformed JSON", lookupKV{value: "{", exists: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := &ETCDStorage{Logger: logrus.New(), mutex: &sync.Mutex{}, kvHandle: tc.kv}
			for name, lookup := range entityLookups(t, sp) {
				t.Run(name, func(t *testing.T) {
					entity, err := lookup()
					require.Error(t, err)
					require.Nil(t, entity)
					if tc.kv.err != nil {
						require.ErrorIs(t, err, backendErr)
					}
				})
			}
		})
	}
}

func TestTransitionFirstPageSnapshot(t *testing.T) {
	for _, disablePaging := range []bool{false, true} {
		t.Run(fmt.Sprintf("disablePaging=%t", disablePaging), func(t *testing.T) {
			sp := &MEMStorage{PageSize: 1}
			require.NoError(t, sp.Init(nil))
			t.Cleanup(func() { require.NoError(t, sp.Close()) })
			etcd := toETCDStorage(sp)
			etcd.DisableSizeChecks = disablePaging
			tr := model.Transition{
				TransitionID: uuid.New(), Status: model.TransitionStatusNew,
				Location: []model.LocationParameter{{Xname: "x0c0s0b0n0"}, {Xname: "x0c0s0b0n1"}},
				TaskIDs:  []uuid.UUID{uuid.New(), uuid.New()},
				Tasks:    []model.TransitionTaskResp{{Xname: "x0c0s0b0n0"}, {Xname: "x0c0s0b0n1"}},
			}
			require.NoError(t, etcd.StoreTransition(tr))
			got, snapshot, err := etcd.GetTransition(tr.TransitionID)
			require.NoError(t, err)
			require.Equal(t, tr.Location, got.Location)
			require.Equal(t, tr.TaskIDs, got.TaskIDs)
			require.Equal(t, tr.Tasks, got.Tasks)
			require.NotEmpty(t, snapshot.Location)
			got.Location[0].Xname = "x1c0s0b0n0"
			got.TaskIDs[0] = uuid.New()
			got.Tasks[0].Xname = "x1c0s0b0n0"
			require.Equal(t, tr.Location[0], snapshot.Location[0])
			require.Equal(t, tr.TaskIDs[0], snapshot.TaskIDs[0])
			require.Equal(t, tr.Tasks[0], snapshot.Tasks[0])
			got.Status = model.TransitionStatusAbortSignaled
			changed, err := etcd.TASTransition(*got, *snapshot)
			require.NoError(t, err)
			require.True(t, changed)
		})
	}
}

type transitionPageErrorKV struct {
	lookupKV
}

func (transitionPageErrorKV) GetRange(string, string) ([]hmetcd.Kvi_KV, error) {
	return nil, errors.New("cannot read transition pages")
}

func TestTransitionPageReadError(t *testing.T) {
	sp := &ETCDStorage{
		Logger: logrus.New(), mutex: &sync.Mutex{},
		kvHandle: transitionPageErrorKV{lookupKV: lookupKV{value: `{}`, exists: true}},
	}
	transition, firstPage, err := sp.GetTransition(uuid.New())
	require.ErrorContains(t, err, "cannot read transition pages")
	require.Nil(t, transition)
	require.Nil(t, firstPage)
}
