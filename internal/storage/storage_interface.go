// MIT License
//
// (C) Copyright [2022-2023,2025] Hewlett Packard Enterprise Development LP
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

package storage

import (
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
)

type StorageProvider interface {
	Init(Logger *logrus.Logger) error
	Ping() error

	GetPowerStatusMaster() (time.Time, error)
	StorePowerStatusMaster(now time.Time) error
	TASPowerStatusMaster(now time.Time, testVal time.Time) (bool, error)
	StorePowerStatus(p model.PowerStatusComponent) error
	DeletePowerStatus(xname string) error
	GetPowerStatus(xname string) (model.PowerStatusComponent, error)
	GetAllPowerStatus() (model.PowerStatus, error)
	GetPowerStatusHierarchy(xname string) (model.PowerStatus, error)

	StorePowerCapTask(task model.PowerCapTask) error
	StorePowerCapOperation(op model.PowerCapOperation) error
	GetPowerCapTask(taskID uuid.UUID) (model.PowerCapTask, error)
	GetPowerCapOperation(taskID uuid.UUID, opID uuid.UUID) (model.PowerCapOperation, error)
	GetAllPowerCapOperationsForTask(taskID uuid.UUID) ([]model.PowerCapOperation, error)
	GetAllPowerCapTasks() ([]model.PowerCapTask, error)
	DeletePowerCapTask(taskID uuid.UUID) error
	DeletePowerCapOperation(taskID uuid.UUID, opID uuid.UUID) error

	StoreTransition(transition model.Transition) error
	StoreTransitionTask(task model.TransitionTask) error
	GetTransition(transitionID uuid.UUID) (transition model.Transition, transtiionFirstPage model.Transition, err error)
	GetTransitionTask(transitionID uuid.UUID, taskID uuid.UUID) (model.TransitionTask, error)
	GetAllTasksForTransition(transitionID uuid.UUID) ([]model.TransitionTask, error)
	GetAllTransitions() ([]model.Transition, error)
	DeleteTransition(transitionID uuid.UUID) error
	DeleteTransitionTask(transitionID uuid.UUID, taskID uuid.UUID) error
	TASTransition(transition model.Transition, testVal model.Transition) (bool, error)
	// Close closes the storage provider and releases any resources it holds.
	Close() error
}

type DistributedLockProvider interface {
	Init(Logger *logrus.Logger) error
	Ping() error
	GetDuration() time.Duration
	DistributedTimedLock(maxLockTime time.Duration) error
	Unlock() error
	// Close closes the distributed lock provider and releases any resources it holds.
	Close() error
}
