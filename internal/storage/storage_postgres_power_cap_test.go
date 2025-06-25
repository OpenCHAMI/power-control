//go:build integration_tests

package storage

import (
	"time"

	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
)

// TestPowerCapTaskGetSet tests setting and getting a single power cap task.
func (s *StorageTestSuite) TestPowerCapTaskGetSet() {
	params := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}

	task := model.NewPowerCapSnapshotTask(params, 20)

	// time handling between golang and postgres has some annoying nuances. https://github.com/jackc/pgx/issues/1195
	// provides some background. tl;dr is that we lose nanosecond precision we do not have go's embedded timezones
	// after retrieval. we should consider handling this elsewhere, but it's a minor issue--the times are correct
	// even if their exact representation differs. setting UTC and truncating to us here allows these to match without
	// duration checks
	task.TaskCreateTime = task.TaskCreateTime.Truncate(time.Microsecond).UTC()
	task.AutomaticExpirationTime = task.AutomaticExpirationTime.Truncate(time.Microsecond).UTC()

	err := s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	gotTask, err := s.sp.GetPowerCapTask(task.TaskID)
	s.Require().NoError(err)

	s.Require().NoError(err)
	s.Require().Equal(task.TaskID, gotTask.TaskID)
	s.Require().Equal(task.Type, gotTask.Type)
	s.Require().Equal(task.TaskCreateTime, gotTask.TaskCreateTime)
	s.Require().Equal(task.AutomaticExpirationTime, gotTask.AutomaticExpirationTime)
	s.Require().Equal(task.TaskStatus, gotTask.TaskStatus)
	s.Require().Equal(task.SnapshotParameters, gotTask.SnapshotParameters)
	s.Require().Equal(task.PatchParameters, gotTask.PatchParameters)

	// these are only set for completed tasks
	s.Require().False(gotTask.IsCompressed)
	s.Require().Empty(gotTask.TaskCounts)
	s.Require().Empty(gotTask.Components)
}

// TestPowerCapTaskGetSet tests setting a single power cap task, updating it following compression, and retrieving the
// compressed task after.
func (s *StorageTestSuite) TestPowerCapTaskCompressedGetSet() {
	params := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}

	task := model.NewPowerCapSnapshotTask(params, 20)
	properType := "ProperType"
	task.Type = properType
	err := s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	// these are faked. Normally domain.compressAndCompleteTask() calculates them from the operation list, but all this
	// really cares about is that they're not empty now, and that the second store operation saves them
	task.IsCompressed = true
	task.TaskCounts = model.PowerCapTaskCounts{
		Total:       10,
		New:         2,
		InProgress:  2,
		Failed:      2,
		Succeeded:   2,
		Unsupported: 2,
	}
	task.Components = []model.PowerCapComponent{
		{
			Xname: "x0c0s0b0n1",
		},
		{
			Xname: "x1c0s0b0n1",
		},
	}

	task.Type = "BogusType"

	err = s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	gotTask, err := s.sp.GetPowerCapTask(task.TaskID)
	s.Require().NoError(err)

	s.Require().NoError(err)
	s.Require().Equal(task.TaskID, gotTask.TaskID)
	// this should _not_ have changed after the second update
	s.Require().Equal(properType, gotTask.Type)
	s.Require().Equal(task.TaskStatus, gotTask.TaskStatus)
	s.Require().Equal(task.SnapshotParameters, gotTask.SnapshotParameters)
	s.Require().Equal(task.PatchParameters, gotTask.PatchParameters)

	s.Require().True(gotTask.IsCompressed)
	s.Require().Equal(task.TaskCounts, gotTask.TaskCounts)
	s.Require().Equal(task.Components, gotTask.Components)
}

// TestPowerCapOperationGetSet tests setting and getting a single power cap operation.
func (s *StorageTestSuite) TestPowerCapOperationGetSet() {
	params := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}
	task := model.NewPowerCapSnapshotTask(params, 20)
	err := s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	snapshotType := "snapshot"
	op := model.NewPowerCapOperation(task.TaskID, snapshotType)

	// how do you get one of these naturally? do you even, or are they always tacked on after retrieval?
	limMax := 100
	limMin := 50
	pup := 25
	op.Component = model.PowerCapComponent{
		Xname: "x0c0s0b0n0",
		Error: "",
		Limits: &model.PowerCapabilities{
			HostLimitMax: &limMax,
			HostLimitMin: &limMin,
			PowerupPower: &pup,
		},
	}

	err = s.sp.StorePowerCapOperation(op)
	s.Require().NoError(err)

	gotOp, err := s.sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	s.Require().NoError(err)

	s.Require().NoError(err)
	s.Require().Equal(op.OperationID, gotOp.OperationID)
	s.Require().Equal(op.TaskID, gotOp.TaskID)
	s.Require().Equal(op.Type, gotOp.Type)
	s.Require().Equal(op.Status, gotOp.Status)
	s.Require().Equal(op.Component, gotOp.Component)

	op.Status = "MysteriousTestStatus"
	op.Type = "MysteriousTestType"
	err = s.sp.StorePowerCapOperation(op)
	s.Require().NoError(err)

	// we should be able to update status, but not other fields
	gotOp, err = s.sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	s.Require().NoError(err)
	s.Require().Equal(snapshotType, gotOp.Type)
	s.Require().Equal("MysteriousTestStatus", gotOp.Status)
}

// TestPowerCapTaskDelete tests deleting a single power cap task.
func (s *StorageTestSuite) TestPowerCapTaskDelete() {
	params := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}
	task := model.NewPowerCapSnapshotTask(params, 20)
	err := s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	_, err = s.sp.GetPowerCapTask(task.TaskID)
	s.Require().NoError(err)

	err = s.sp.DeletePowerCapTask(task.TaskID)
	s.Require().NoError(err)

	_, err = s.sp.GetPowerCapTask(task.TaskID)
	s.Require().Error(err)
	s.Require().ErrorContains(err, "could not retrieve power cap task")

}

// TestPowerCapTaskDelete tests deleting a single power cap operation.
func (s *StorageTestSuite) TestPowerCapOperationDelete() {
	params := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}
	task := model.NewPowerCapSnapshotTask(params, 20)
	err := s.sp.StorePowerCapTask(task)
	s.Require().NoError(err)

	op := model.NewPowerCapOperation(task.TaskID, "snapshot")

	err = s.sp.StorePowerCapOperation(op)
	s.Require().NoError(err)

	_, err = s.sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	s.Require().NoError(err)

	err = s.sp.DeletePowerCapOperation(task.TaskID, op.OperationID)
	s.Require().NoError(err)

	_, err = s.sp.GetPowerCapOperation(task.TaskID, op.OperationID)
	s.Require().Error(err)
	s.Require().ErrorContains(err, "could not retrieve power cap operation")
}

// TestPowerCapMultiple tests inserting and retrieving multiple associated resources.
func (s *StorageTestSuite) TestPowerCapMultiple() {
	paramsA := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
	}
	taskA := model.NewPowerCapSnapshotTask(paramsA, 20)
	err := s.sp.StorePowerCapTask(taskA)
	s.Require().NoError(err)

	paramsB := model.PowerCapSnapshotParameter{
		Xnames: []string{"x0c0s0b0n1", "x0c0s1b0n1"},
	}
	taskB := model.NewPowerCapSnapshotTask(paramsB, 20)
	err = s.sp.StorePowerCapTask(taskB)
	s.Require().NoError(err)

	opA1 := model.NewPowerCapOperation(taskA.TaskID, "alpha")
	opA2 := model.NewPowerCapOperation(taskA.TaskID, "bravo")

	opB1 := model.NewPowerCapOperation(taskB.TaskID, "xray")
	opB2 := model.NewPowerCapOperation(taskB.TaskID, "yankee")
	opB3 := model.NewPowerCapOperation(taskB.TaskID, "zulu")

	for _, op := range []model.PowerCapOperation{opA1, opA2, opB1, opB2, opB3} {
		err := s.sp.StorePowerCapOperation(op)
		s.Require().NoError(err)
	}

	tasks, err := s.sp.GetAllPowerCapTasks()
	s.Require().NoError(err)
	s.Require().Len(tasks, 2)

	opsA, err := s.sp.GetAllPowerCapOperationsForTask(taskA.TaskID)
	s.Require().NoError(err)
	s.Require().Len(opsA, 2)

	opsB, err := s.sp.GetAllPowerCapOperationsForTask(taskB.TaskID)
	s.Require().NoError(err)
	s.Require().Len(opsB, 3)

	s.Require().Contains([]string{opsA[0].Type, opsA[1].Type}, "alpha")
	s.Require().Contains([]string{opsA[0].Type, opsA[1].Type}, "bravo")
	s.Require().Contains([]string{opsB[0].Type, opsB[1].Type, opsB[2].Type}, "xray")
	s.Require().Contains([]string{opsB[0].Type, opsB[1].Type, opsB[2].Type}, "yankee")
	s.Require().Contains([]string{opsB[0].Type, opsB[1].Type, opsB[2].Type}, "zulu")
}
