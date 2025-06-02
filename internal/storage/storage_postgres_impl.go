package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/OpenCHAMI/power-control/v2/internal/model"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
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

	connStr := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s", config.Host, config.Port, config.DBName, config.User, config.Password, sslmode)
	if config.Opts != "" {
		connStr += " " + config.Opts
	}

	// Connect to postgres, looping every retryWait seconds up to retryCount times.
	for ; ix <= config.RetryCount; ix++ {
		log.Printf("Attempting connection to Postgres at %s:%d (attempt %d)", config.Host, config.Port, ix)

		db, err = sql.Open("postgres", connStr)
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
	return nil
}

func (p *PostgresStorage) StoreTransitionTask(op model.TransitionTask) error {
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

func (p *PostgresStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
