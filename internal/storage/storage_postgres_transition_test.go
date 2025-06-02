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
	err = s.sp.StoreTransition(testTransition)
	s.Require().NoError(err)
}
