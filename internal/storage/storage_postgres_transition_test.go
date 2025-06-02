//go:build integration_tests

package storage

import (
	"github.com/OpenCHAMI/power-control/v2/internal/model"

	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func (s *StorageTestSuite) TestTransitionSet() {
	t := s.T()
	var (
		testParams     model.TransitionParameter
		testTransition model.Transition
		err            error
	)

	testParams = model.TransitionParameter{
		Operation: "Init",
		Location: []model.LocationParameter{
			model.LocationParameter{Xname: "x0c0s1b0n0"},
			model.LocationParameter{Xname: "x0c0s2b0n0"},
			model.LocationParameter{Xname: "x0c0s1"},
			model.LocationParameter{Xname: "x0c0s2"},
		},
	}

	t.Logf("inserting some transitions and tasks")
	testTransition, _ = model.ToTransition(testParams, 5)
	testTransition.Status = model.TransitionStatusInProgress
	task := model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Waiting
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Sending
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)

	// This preserves the original "store tasks, then store transition that owns those tasks" order of the snippet of
	// domain.TestDoTransition() used to source this test. This _does not_ allow enforcing foreign key constraints
	// on the task table schema. It's unclear if this is just weirdness in the test, or if actual operation needs
	// that same flexibility. We'll probably want to try with a proper foreign key in the future, to see if PCS crashes
	// and burns.
	err = s.sp.StoreTransition(testTransition)
	s.Require().NoError(err)

	// discards the first page
	gotTransition, _, err := s.sp.GetTransition(testTransition.TransitionID)
	s.Require().NoError(err)
	s.Require().Equal(testTransition.TransitionID, gotTransition.TransitionID)
	s.Require().Equal(testTransition.Operation, gotTransition.Operation)
	s.Require().Equal(testTransition.TaskDeadline, gotTransition.TaskDeadline)

	gotTask, err := s.sp.GetTransitionTask(task.TransitionID, task.TaskID)
	s.Require().NoError(err)
	s.Require().Equal(task, gotTask)
}
