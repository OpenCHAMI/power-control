package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Cray-HPE/hms-xname/xnametypes"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	pq "github.com/lib/pq"
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
	if p.db == nil {
		return fmt.Errorf("instance closed or not initialized")
	}

	if err := p.db.Ping(); err != nil {
		return fmt.Errorf("ping failed: %v", err)
	}

	return nil
}

func (p *PostgresStorage) GetPowerStatusMaster() (lastUpdated time.Time, err error) {
	exec := `SELECT last_updated FROM power_status_master`
	err = p.db.Get(&lastUpdated, exec)

	if err != nil {
		// If the error is sql.ErrNoRows, it means not power status master exists, we have to return a specific error message!
		// This is a workaround for the fact that the ETCD implementation returns a specific error message when the power status
		// master does not exist. This should be reworked in the future!
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, errors.New("power status master does not exist")
		}

		return time.Time{}, fmt.Errorf("failed to get power status master: %w", err)
	}

	return lastUpdated, nil
}

func (p *PostgresStorage) StorePowerStatusMaster(now time.Time) error {
	exec := `
		INSERT INTO power_status_master (last_updated)
		VALUES ($1)
		ON CONFLICT (singleton) DO UPDATE SET
			last_updated = EXCLUDED.last_updated
	`
	_, err := p.db.Exec(exec, now)
	if err != nil {
		return fmt.Errorf("failed to store power status master timestamp: %w", err)
	}

	return nil
}

func (p *PostgresStorage) TASPowerStatusMaster(now time.Time, testVal time.Time) (bool, error) {
	exec := `
		INSERT INTO power_status_master (last_updated)
		VALUES ($1)
		ON CONFLICT (singleton) DO UPDATE SET
			last_updated = EXCLUDED.last_updated
		WHERE power_status_master.last_updated = $2
	`

	result, err := p.db.Exec(exec, now, testVal)
	if err != nil {
		return false, fmt.Errorf("failed to store power status master timestamp: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

func (p *PostgresStorage) StorePowerStatus(psc model.PowerStatusComponent) error {
	if !(xnametypes.IsHMSCompIDValid(psc.XName)) {
		return fmt.Errorf("invalid xname: %s", psc.XName)
	}

	exec := `
		INSERT INTO power_status_component (
			xname, 
			power_state, 
			management_state, 
			error, 
			supported_power_transitions, 
			last_updated
		)
		VALUES (
			:xname, 
			:power_state, 
			:management_state, 
			:error, 
			:supported_power_transitions, 
			:last_updated
		)
		ON CONFLICT (xname) DO UPDATE SET 
			power_state = excluded.power_state,
			management_state = excluded.management_state,
			error = excluded.error,
			supported_power_transitions = excluded.supported_power_transitions,
			last_updated = excluded.last_updated
	`

	// Convert model.PowerStatusComponent to powerStatusComponentDB for database storage
	var pscDB powerStatusComponentDB
	pscDB.fromPowerStatusComponent(psc)

	_, err := p.db.NamedExec(
		exec, pscDB,
	)
	if err != nil {
		return fmt.Errorf("failed to store power status component for '%s': %w", psc.XName, err)
	}

	return nil
}

func (p *PostgresStorage) DeletePowerStatus(xname string) error {
	if !(xnametypes.IsHMSCompIDValid(xname)) {
		return fmt.Errorf("invalid xname: %s", xname)
	}

	exec := `DELETE FROM power_status_component WHERE xname = $1`
	_, err := p.db.Exec(exec, xname)
	if err != nil {
		return fmt.Errorf("failed to delete power status component for '%s': %w", xname, err)
	}

	return nil
}

// Wrapper struct to override the SupportedPowerTransitions field in model.PowerStatusComponent
type powerStatusComponentDB struct {
	model.PowerStatusComponent
	// Override the field with type alias
	SupportedPowerTransitions pq.StringArray `json:"supportedPowerTransitions" db:"supported_power_transitions"`
}

// toPowerStatusComponent converts a database representation (powerStatusComponentDB) to a model.PowerStatusComponent
func (pscDB *powerStatusComponentDB) toPowerStatusComponent() model.PowerStatusComponent {
	psc := pscDB.PowerStatusComponent
	psc.SupportedPowerTransitions = []string(pscDB.SupportedPowerTransitions)

	return psc
}

// fromPowerStatusComponent converts a model.PowerStatusComponent to a database representation (powerStatusComponentDB)
func (pscDB *powerStatusComponentDB) fromPowerStatusComponent(psc model.PowerStatusComponent) {
	pscDB.PowerStatusComponent = psc
	pscDB.SupportedPowerTransitions = pq.StringArray(psc.SupportedPowerTransitions)
}

func (p *PostgresStorage) GetPowerStatus(xname string) (psc model.PowerStatusComponent, err error) {
	if !(xnametypes.IsHMSCompIDValid(xname)) {
		return psc, fmt.Errorf("invalid xname: %s", xname)
	}

	var pscDB powerStatusComponentDB

	err = p.db.Get(&pscDB, "SELECT * FROM power_status_component WHERE xname = $1", xname)
	if err != nil {

		return model.PowerStatusComponent{}, err
	}

	// Convert to model struct
	psc = pscDB.toPowerStatusComponent()

	return psc, nil
}

// toPowerStatusComponents converts a slice of powerStatusComponentDB to a slice of model.PowerStatusComponent
func toPowerStatusComponents(pscDBs []powerStatusComponentDB) []model.PowerStatusComponent {
	psc := make([]model.PowerStatusComponent, len(pscDBs))
	for i, pscDB := range pscDBs {
		psc[i] = pscDB.toPowerStatusComponent()
	}

	return psc
}

func (p *PostgresStorage) GetAllPowerStatus() (ps model.PowerStatus, err error) {
	status := []powerStatusComponentDB{}

	err = p.db.Select(&status, "SELECT * FROM power_status_component")
	if err != nil {
		return model.PowerStatus{}, fmt.Errorf("failed to get all power status components: %w", err)
	}

	// Convert the slice of powerStatusComponentDB to model.PowerStatus
	ps.Status = toPowerStatusComponents(status)

	return ps, nil
}

func (p *PostgresStorage) GetPowerStatusHierarchy(xname string) (ps model.PowerStatus, err error) {
	if !(xnametypes.IsHMSCompIDValid(xname)) {
		return ps, fmt.Errorf("invalid xname: %s", xname)
	}

	status := []powerStatusComponentDB{}
	err = p.db.Select(&status, "SELECT * FROM power_status_component WHERE xname LIKE $1 || '%'", xname)
	if err != nil {
		return model.PowerStatus{}, fmt.Errorf("failed to get power status hierarchy for '%s': %w", xname, err)
	}

	// Convert the slice of powerStatusComponentDB to model.PowerStatusstatus
	ps.Status = toPowerStatusComponents(status)

	return ps, nil
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
