package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Cray-HPE/hms-xname/xnametypes"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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
		// If the error is sql.ErrNoRows, no power status master exists.
		// domain:getPowerStatusMaster() relies on the storage engine errors containing
		// "power status master does not exist" for part of its control flow.
		// We should consider indicating this condition explicitly in the interface function signature instead.
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
