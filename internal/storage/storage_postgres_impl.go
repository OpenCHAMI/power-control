package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
)

type PostgresConfig struct {
	Host       string
	User       string
	DBName     string
	Password   string
	Port       uint
	RetryCount uint64
	RetryWait  uint64
	Insecure   bool
	Opts       string
	ConnStr    string
}

func DefaultPostgresConfig() PostgresConfig {
	return PostgresConfig{
		Host:       "localhost",
		User:       "pcsuser",
		Port:       uint(5432),
		RetryCount: uint64(5),
		RetryWait:  uint64(10),
		Insecure:   false,
		DBName:     "pcsdb",
	}
}

type PostgresStorage struct {
	db     *sqlx.DB
	logger *logrus.Logger
	Config PostgresConfig
}

func OpenDB(config PostgresConfig, log *logrus.Logger) (*sql.DB, error) {
	var (
		err     error
		db      *sql.DB
		sslmode string
		ix      = uint64(1)
	)

	if log == nil {
		log = logrus.New()
	}

	if !config.Insecure {
		sslmode = "verify-full"
	} else {
		sslmode = "disable"
	}

	if config.ConnStr == "" {
		config.ConnStr = fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s", config.Host, config.Port, config.DBName, config.User, config.Password, sslmode)
		if config.Opts != "" {
			config.ConnStr += " " + config.Opts
		}
	}

	// Connect to postgres, looping every retryWait seconds up to retryCount times.
	for ; ix <= config.RetryCount; ix++ {
		log.Printf("Attempting connection to Postgres at %s:%d (attempt %d)", config.Host, config.Port, ix)

		db, err = sql.Open("postgres", config.ConnStr)
		if err != nil {
			log.Printf("ERROR: failed to open connection to Postgres at %s:%d (attempt %d, retrying in %d seconds): %v\n", config.Host, config.Port, ix, config.RetryWait, err)
		} else {
			break
		}

		time.Sleep(time.Duration(config.RetryWait) * time.Second)
	}
	if ix > config.RetryCount {
		err = fmt.Errorf("postgres connection attempts exhausted (%d)", config.RetryCount)
	} else {
		log.Printf("Initialized connection to Postgres database at %s:%d", config.Host, config.Port)
	}

	// Ping postgres, looping every retryWait seconds up to retryCount times.
	for ; ix <= config.RetryCount; ix++ {
		log.Printf("Attempting to ping Postgres connection at %s:%d (attempt %d)", config.Host, config.Port, ix)

		err = db.Ping()
		if err != nil {
			log.Printf("ERROR: failed to ping Postgres at %s:%d (attempt %d, retrying in %d seconds): %v\n", config.Host, config.Port, ix, config.RetryWait, err)
		} else {
			break
		}

		time.Sleep(time.Duration(config.RetryWait) * time.Second)
	}
	if ix > config.RetryCount {
		err = fmt.Errorf("postgres ping attempts exhausted (%d)", config.RetryCount)
	} else {
		log.Printf("Pinged Postgres database at %s:%d", config.Host, config.Port)
	}

	return db, err
}

func (p *PostgresStorage) Init(logger *logrus.Logger) error {
	p.logger = logger
	p.db = nil
	db, err := OpenDB(p.Config, logger)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	p.db = sqlx.NewDb(db, "postgres")

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

func (p *PostgresStorage) StoreTransition(transition model.Transition) error {
	// should update conflicts be able to update anything other than active and status? technically IDK if there's any
	// expectation otherwise and etcd would just update the whole damn thing for a given key, but changing the
	// operation and such after creation seems wrong, and potentially catastrophic
	tx, err := p.db.Beginx()
	if err != nil {
		return fmt.Errorf("could not begin transaction for transition '%s': %w", transition.TransitionID, err)
	}
	defer tx.Rollback()

	err = storeTransitionWithTx(tx, transition)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("Failed to commit transition '%s': %w", transition.TransitionID, err)
	}
	return nil
}

// storeTransitionWithTx is a helper that upserts a Transition and its Locations within a given transaction. The caller
// is responsible for committing or rolling back the transaction.
func storeTransitionWithTx(tx *sqlx.Tx, transition model.Transition) error {
	exec := `INSERT INTO transitions (
		id,
		operation,
		deadline,
		created,
		active,
		expires,
		location,
		status
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (id) DO UPDATE SET active = excluded.active, status = excluded.status`
	_, err := tx.Exec(
		exec,
		transition.TransitionID,
		transition.Operation,
		transition.TaskDeadline,
		transition.CreateTime,
		transition.LastActiveTime,
		transition.AutomaticExpirationTime,
		transition.Location,
		transition.Status,
	)
	if err != nil {
		return fmt.Errorf("Failed to store transition '%s': %w", transition.TransitionID, err)
	}
	return nil
}

func (p *PostgresStorage) StoreTransitionTask(op model.TransitionTask) error {
	exec := `INSERT INTO transition_tasks (
		id,
		transition_id,
		operation,
		state,
		xname,
		reservation_key,
		deputy_key,
		status,
		status_desc,
		error
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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

// the first page here is very etcd leaky abstraction. etcd GetTransition
// returns both the complete transition and its first page if paging is
// enabled. This first page is often discarded by downstream functions. AFAICT
// it's kept only when the function will perform a TAS afterwards, because the
// TAS relies on etcd-side comparisons and won't be able to compare against the
// full transition properly. This is kinda silly, but I guess the upshot is we
// don't really need to worry about it and can just return the same object
// twice, as it will just get fed back into our own TAS.

//

func (p *PostgresStorage) GetTransition(transitionID uuid.UUID) (transition model.Transition, transitionFirstPage model.Transition, err error) {
	err = p.db.Get(&transition, "SELECT * FROM transitions WHERE id = $1", transitionID)
	if err != nil {
		return model.Transition{}, model.Transition{}, err
	}
	return transition, transition, nil
}

// more etcd leakage. this needs the transition ID because you can't build
// the etcd key for a task without the transition that owns it

func (p *PostgresStorage) GetTransitionTask(_ uuid.UUID, taskID uuid.UUID) (model.TransitionTask, error) {
	var task model.TransitionTask
	err := p.db.Get(&task, "SELECT * FROM transition_tasks WHERE id = $1", taskID)
	if err != nil {
		return model.TransitionTask{}, err
	}
	return task, nil
}

func (p *PostgresStorage) GetAllTasksForTransition(transitionID uuid.UUID) ([]model.TransitionTask, error) {
	tasks := []model.TransitionTask{}
	err := p.db.Select(&tasks, "SELECT * FROM transition_tasks WHERE transition_id = $1", transitionID)
	if err != nil {
		return []model.TransitionTask{}, err
	}
	return tasks, nil
}

func (p *PostgresStorage) GetAllTransitions() ([]model.Transition, error) {
	transitions := []model.Transition{}
	err := p.db.Select(&transitions, "SELECT * FROM transitions")
	if err != nil {
		return []model.Transition{}, err
	}
	return transitions, nil
}

func (p *PostgresStorage) DeleteTransition(transitionID uuid.UUID) error {
	_, err := p.db.Exec("DELETE FROM transitions WHERE id = $1", transitionID)
	return err
}

func (p *PostgresStorage) DeleteTransitionTask(transitionID uuid.UUID, taskID uuid.UUID) error {
	_, err := p.db.Exec("DELETE FROM transition_tasks WHERE id = $1", taskID)
	return err
}

func (p *PostgresStorage) TASTransition(transition model.Transition, testVal model.Transition) (bool, error) {
	tx, err := p.db.Beginx()
	if err != nil {
		return false, fmt.Errorf("could not begin TAS transaction: %w", err)
	}
	defer tx.Rollback()
	var current model.Transition
	err = tx.Get(&current, "SELECT * FROM transitions WHERE id = $1", transition.TransitionID)
	if err != nil {
		return false, fmt.Errorf("could retrieve TAS transition: %w", err)
	}
	// Location is not comparable. I'm unsure if we'd want to do a set equality check on it (AFAIK it is a set in
	// practice). The etcd implementation _does not_ check all pages, so it de facto ignores Locations.
	if cmp.Equal(testVal, current, cmpopts.IgnoreFields(model.Transition{}, "Location")) {
		_, err := tx.Exec("DELETE FROM transitions WHERE id = $1", transition.TransitionID)
		if err != nil {
			return false, fmt.Errorf("could not replace TAS transition: %w", err)
		}
		err = storeTransitionWithTx(tx, transition)
		if err != nil {
			return false, fmt.Errorf("could not replace TAS transition: %w", err)
		}
		if err = tx.Commit(); err != nil {
			return false, fmt.Errorf("could not commit TAS transition: %w", err)
		}
	} else {
		return false, nil
	}

	return true, nil
}

func (p *PostgresStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
