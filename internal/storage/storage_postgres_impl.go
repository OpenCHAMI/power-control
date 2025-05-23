package storage

import (
	"fmt"
	"time"

	"github.com/OpenCHAMI/power-control/v2/internal/model"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

type PostgresStorage struct {
	logger *logrus.Logger
	db     *sqlx.DB
}

func (p *PostgresStorage) Init(Logger *logrus.Logger) error {
	return nil
}

func (p *PostgresStorage) Ping() error {
	return nil
}

func (p *PostgresStorage) GetPowerStatusMaster() (time.Time, error) {
	return time.Time{}, nil
}

func (p *PostgresStorage) StorePowerStatusMaster(now time.Time) error {
	return nil
}

func (p *PostgresStorage) TASPowerStatusMaster(now time.Time, testVal time.Time) (bool, error) {
	return false, nil
}

func (p *PostgresStorage) StorePowerStatus(psc model.PowerStatusComponent) error {
	return nil
}

func (p *PostgresStorage) DeletePowerStatus(xname string) error {
	return nil
}

func (p *PostgresStorage) GetPowerStatus(xname string) (model.PowerStatusComponent, error) {
	return model.PowerStatusComponent{}, nil
}

func (p *PostgresStorage) GetAllPowerStatus() (model.PowerStatus, error) {
	return model.PowerStatus{}, nil
}

func (p *PostgresStorage) GetPowerStatusHierarchy(xname string) (model.PowerStatus, error) {
	return model.PowerStatus{}, nil
}

func (p *PostgresStorage) StorePowerCapTask(task model.PowerCapTask) error {
	return nil
}

func (p *PostgresStorage) StorePowerCapOperation(op model.PowerCapOperation) error {
	return nil
}

func (p *PostgresStorage) GetPowerCapTask(taskID uuid.UUID) (model.PowerCapTask, error) {
	return model.PowerCapTask{}, nil
}

func (p *PostgresStorage) GetPowerCapOperation(taskID uuid.UUID, opID uuid.UUID) (model.PowerCapOperation, error) {
	return model.PowerCapOperation{}, nil
}

func (p *PostgresStorage) GetAllPowerCapOperationsForTask(taskID uuid.UUID) ([]model.PowerCapOperation, error) {
	return nil, nil
}

func (p *PostgresStorage) GetAllPowerCapTasks() ([]model.PowerCapTask, error) {
	return nil, nil
}

func (p *PostgresStorage) DeletePowerCapTask(taskID uuid.UUID) error {
	return nil
}

func (p *PostgresStorage) DeletePowerCapOperation(taskID uuid.UUID, opID uuid.UUID) error {
	return nil
}

// TRC StoreTransition currently sets a key from a transition prefix and the transition ID

func (p *PostgresStorage) StoreTransition(transition model.Transition) error {
	// should update conflicts be able to update anything other than active and status? technically IDK if there's any
	// expectation otherwise and etcd would just update the whole damn thing for a given key, but changing the
	// operation and such after creation seems wrong, and potentially catastrophic
	exec := `INSERT INTO transitions (id, operation, deadline, created, active, expires, status)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (id) DO UPDATE SET active = excluded.active, status = excluded.status`
	_, err := p.db.Exec(
		exec,
		transition.TransitionID,
		transition.Operation,
		transition.TaskDeadline,
		transition.CreateTime,
		transition.AutomaticExpirationTime,
		transition.LastActiveTime,
		transition.Status,
	)
	if err != nil {
		return fmt.Errorf("Failed to store transition '%s': %w", transition.TransitionID, err)
	}
	return nil
}

func (p *PostgresStorage) StoreTransitionTask(op model.TransitionTask) error {
	exec := `INSERT INTO transition_tasks (id, transition_id, operation, state, xname, reservation_key, deputy_key, status, status_desc, error)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (id) DO UPDATE SET state = excluded.state, status = excluded.status, status_desc = excluded.status_desc, error = excluded.error`
	_, err := p.db.Exec(
		exec,
		op.TaskID,
		op.TransitionID,
		op.Operation,
		op.State,
		op.Xname,
		op.ReservationKey,
		op.DeputyKey,
		op.Status,
		op.StatusDesc,
		op.Error,
	)
	if err != nil {
		return fmt.Errorf("Failed to store task '%s': %w", op.TaskID, err)
	}
	return nil
}

func (p *PostgresStorage) GetTransition(transitionID uuid.UUID) (transition model.Transition, transitionFirstPage model.Transition, err error) {
	return model.Transition{}, model.Transition{}, nil
}

func (p *PostgresStorage) GetTransitionTask(transitionID uuid.UUID, taskID uuid.UUID) (model.TransitionTask, error) {
	return model.TransitionTask{}, nil
}

func (p *PostgresStorage) GetAllTasksForTransition(transitionID uuid.UUID) ([]model.TransitionTask, error) {
	return nil, nil
}

func (p *PostgresStorage) GetAllTransitions() ([]model.Transition, error) {
	return nil, nil
}

func (p *PostgresStorage) DeleteTransition(transitionID uuid.UUID) error {
	return nil
}

func (p *PostgresStorage) DeleteTransitionTask(transitionID uuid.UUID, taskID uuid.UUID) error {
	return nil
}

func (p *PostgresStorage) TASTransition(transition model.Transition, testVal model.Transition) (bool, error) {
	return false, nil
}
