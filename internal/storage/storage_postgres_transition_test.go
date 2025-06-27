//go:build integration_tests

package storage

import (
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"golang.org/x/exp/slices"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
)

// TestTransitionSetGet tests setting and getting a single transition with several attached tasks.
func (s *StorageTestSuite) TestTransitionSetGet() {
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

	record := map[uuid.UUID]string{}

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
	record[task.TaskID] = task.Xname

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Sending
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)
	record[task.TaskID] = task.Xname

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)
	record[task.TaskID] = task.Xname

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)
	record[task.TaskID] = task.Xname

	// This preserves the original "store tasks, then store transition that owns those tasks" order of the snippet of
	// domain.TestDoTransition() used to source this test. This _does not_ allow enforcing foreign key constraints
	// on the task table schema. It's unclear if this is just weirdness in the test, or if actual operation needs
	// that same flexibility. We'll probably want to try with a proper foreign key in the future, to see if PCS crashes
	// and burns.
	err = s.sp.StoreTransition(testTransition)
	s.Require().NoError(err)

	// discards the first page. there isn't really anything we can do with this in tests, it's a leaky abstraction
	// for etcd.
	gotTransition, _, err := s.sp.GetTransition(testTransition.TransitionID)
	s.Require().NoError(err)
	s.Require().Equal(testTransition.TransitionID, gotTransition.TransitionID)
	s.Require().Equal(testTransition.Operation, gotTransition.Operation)
	s.Require().Equal(testTransition.TaskDeadline, gotTransition.TaskDeadline)
	s.Require().True(slices.Equal(testTransition.Location, gotTransition.Location))

	gotTask, err := s.sp.GetTransitionTask(task.TransitionID, task.TaskID)
	s.Require().NoError(err)
	s.Require().Equal(task, gotTask)

	gotTasks, err := s.sp.GetAllTasksForTransition(testTransition.TransitionID)
	for _, t := range gotTasks {
		s.Assert().Equal(record[t.TaskID], t.Xname)
	}
}

// TestMultipleTransitions tests setting and getting multiple transitions.
func (s *StorageTestSuite) TestMultipleTransitions() {
	t := s.T()

	testParamsA := model.TransitionParameter{
		Operation: "Init",
		Location: []model.LocationParameter{
			model.LocationParameter{Xname: "x0c0s1b0n0"},
		},
	}

	testParamsB := model.TransitionParameter{
		Operation: "HardRestart",
		Location: []model.LocationParameter{
			model.LocationParameter{Xname: "x1c0s1b0n0"},
		},
	}

	record := map[uuid.UUID]model.Operation{}

	t.Logf("inserting some transitions and tasks")
	testTransitionA, _ := model.ToTransition(testParamsA, 5)
	testTransitionA.Status = model.TransitionStatusInProgress
	taskA := model.NewTransitionTask(testTransitionA.TransitionID, testTransitionA.Operation)
	taskA.Xname = "x0c0s1b0n0"
	taskA.Operation = model.Operation_Off
	taskA.State = model.TaskState_Waiting
	testTransitionA.TaskIDs = append(testTransitionA.TaskIDs, taskA.TaskID)
	err := s.sp.StoreTransitionTask(taskA)
	s.Require().NoError(err)
	record[testTransitionA.TransitionID] = testTransitionA.Operation

	err = s.sp.StoreTransition(testTransitionA)
	s.Require().NoError(err)

	testTransitionB, _ := model.ToTransition(testParamsB, 5)
	testTransitionB.Status = model.TransitionStatusInProgress
	taskB := model.NewTransitionTask(testTransitionB.TransitionID, testTransitionB.Operation)
	taskB.Xname = "x1c0s1b0n0"
	taskB.Operation = model.Operation_Off
	taskB.State = model.TaskState_Waiting
	testTransitionB.TaskIDs = append(testTransitionB.TaskIDs, taskB.TaskID)
	err = s.sp.StoreTransitionTask(taskB)
	s.Require().NoError(err)
	record[testTransitionB.TransitionID] = testTransitionB.Operation

	err = s.sp.StoreTransition(testTransitionB)
	s.Require().NoError(err)

	gotTasks, err := s.sp.GetAllTasksForTransition(testTransitionA.TransitionID)
	s.Require().NoError(err)
	s.Require().Len(gotTasks, 1)
	s.Require().Equal(taskA.TaskID, gotTasks[0].TaskID)

	gotTasks, err = s.sp.GetAllTasksForTransition(testTransitionB.TransitionID)
	s.Require().NoError(err)
	s.Require().Len(gotTasks, 1)
	s.Require().Equal(taskB.TaskID, gotTasks[0].TaskID)

	gotTransitions, err := s.sp.GetAllTransitions()
	s.Require().NoError(err)
	for _, t := range gotTransitions {
		s.Assert().Equal(record[t.TransitionID], t.Operation)
	}
}

// TestTransitionDelete tests setting and deleting a single transition with an attached task.
func (s *StorageTestSuite) TestTransitionDelete() {
	t := s.T()
	testParams := model.TransitionParameter{
		Operation: "Init",
		Location: []model.LocationParameter{
			model.LocationParameter{Xname: "x0c0s1b0n0"},
			model.LocationParameter{Xname: "x0c0s2b0n0"},
			model.LocationParameter{Xname: "x0c0s1"},
			model.LocationParameter{Xname: "x0c0s2"},
		},
	}

	t.Logf("inserting a transitions and task")
	testTransition, _ := model.ToTransition(testParams, 5)
	testTransition.Status = model.TransitionStatusInProgress
	task := model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Waiting
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err := s.sp.StoreTransitionTask(task)
	s.Require().NoError(err)

	err = s.sp.StoreTransition(testTransition)
	s.Require().NoError(err)

	s.sp.DeleteTransition(testTransition.TransitionID)
	s.sp.DeleteTransitionTask(testTransition.TransitionID, task.TaskID)

	gotTasks, err := s.sp.GetAllTasksForTransition(testTransition.TransitionID)
	s.Require().NoError(err)
	s.Require().Len(gotTasks, 0)

	gotTransitions, err := s.sp.GetAllTransitions()
	s.Require().NoError(err)
	for _, t := range gotTransitions {
		s.Assert().NotEqual(t.TransitionID, testTransition.TransitionID)
	}
}

// TestTransitionTAS tests test and set operations on transitions.
func (s *StorageTestSuite) TestTransitionTAS() {
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
	err = s.sp.StoreTransition(testTransition)
	s.Require().NoError(err)

	// discards the first page. there isn't really anything we can do with this in tests, it's a leaky abstraction
	// for etcd.
	t.Logf("retrieving transition %s", testTransition.TransitionID)
	gotTransition, _, err := s.sp.GetTransition(testTransition.TransitionID)
	s.Require().NoError(err)
	s.Require().True(slices.Equal(testTransition.Location, gotTransition.Location))

	modTransition := gotTransition
	modTransition.Status = model.TransitionStatusAborted

	// nothing should have touched the original, this change should succeed
	t.Logf("updating transition %s", testTransition.TransitionID)
	changed, err := s.sp.TASTransition(modTransition, gotTransition)
	s.Require().NoError(err)
	s.Require().True(changed)

	// the change succeeded, testing against the original should now fail
	modTransition.Status = model.TransitionStatusNew
	changed, err = s.sp.TASTransition(modTransition, gotTransition)
	s.Require().NoError(err)
	s.Require().False(changed)

	// the transition in the db should reflect the first change
	gotTransition, _, err = s.sp.GetTransition(testTransition.TransitionID)
	s.Require().NoError(err)
	s.Require().Equal(gotTransition.Status, model.TransitionStatusAborted)

	modTransition.TransitionID = uuid.New()
	t.Logf("failing to update non-existent transition %s", modTransition.TransitionID)
	changed, err = s.sp.TASTransition(modTransition, gotTransition)
	s.Require().ErrorContains(err, "could retrieve TAS transition")
	s.Require().False(changed)
}
