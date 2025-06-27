package storage

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

// constants used to generate unique IDs for advisory locks
const advisoryLockNamespace = "pcs"
const advisoryDistLockName = "distlock"

// advisoryLock hold the state of a advisory lock
type advisoryLock struct {
	// db is the database connection pool used to acquire the advisory lock
	db *sqlx.DB
	// tx is the transaction that holds the advisory lock, we hold on
	// to the transaction to ensure that we can release the lock on
	// the same db connection. Without holding this or the connection our
	// unlock may occur on a different connection, which would not release the lock.
	tx *sqlx.Tx
	// namespaceID and lockID are unique identifiers for the advisory lock
	namespaceID int32
	lockID      int32
	// timeout is the duration to wait to acquire the lock
	timeout time.Duration
	// mutex is used to synchronize access to the lock state
	mutex sync.Mutex
	// logger is used for logging messages related to the advisory lock
	logger *logrus.Logger
}

// generateID generates a unique ID for a string using FNV-1a hash algorithm.
func generateID(s string) int32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int32(h.Sum32())
}

// newTimedAdvisoryLock creates a new timed advisory lock with the given namespace and lock name.
func (p *PostgresLockProvider) newTimedAdvisoryLock(namespace string, lock string, timeout time.Duration) (*advisoryLock, error) {
	if timeout < time.Second {
		return nil, fmt.Errorf("timeout must be >= 1 second")
	}

	// We could save these, but FNV is fast enough to generate them on the fly
	namespaceID := generateID(namespace)
	lockID := generateID(lock)

	advisoryLock := &advisoryLock{
		db:          p.db,
		namespaceID: namespaceID,
		lockID:      lockID,
		timeout:     timeout,
		logger:      p.logger,
	}

	return advisoryLock, nil
}

func (l *advisoryLock) acquire() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.tx != nil {
		return fmt.Errorf("advisory lock already held")
	}

	// create a context with the lock acquisition timeout
	timeoutCtx, timeoutCtxCancel := context.WithTimeout(context.Background(), l.timeout)
	defer timeoutCtxCancel()

	// begin a transaction for the advisory lock, the lock is scoped to the transaction
	tx, err := l.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	// acquire the advisory lock using pg_advisory_xact_lock
	_, err = tx.ExecContext(timeoutCtx, "SELECT pg_advisory_xact_lock($1, $2)", l.namespaceID, l.lockID)
	if err != nil {
		tx.Rollback()

		return err
	}

	// Save the transaction in the lock state, we need it to release the lock later
	l.tx = tx

	return nil
}

// release releases the advisory lock and cleans up resources.
func (l *advisoryLock) release() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.tx == nil {
		return fmt.Errorf("advisory lock not held")
	}

	// release the advisory lock by rolling back the transaction
	err := l.tx.Rollback()
	if err != nil {
		return fmt.Errorf("failed to release advisory lock: %w", err)
	}

	// reset the lock state
	l.tx = nil

	return nil
}

// PostgresLockProvider implements a distributed lock provider using PostgreSQL's advisory locks.
type PostgresLockProvider struct {
	// Config holds the postgres configuration
	Config PostgresConfig
	// logger is used for logging messages
	logger *logrus.Logger
	// db is the database connection used to acquire advisory locks
	db *sqlx.DB
	// lock is the current advisory lock held by this provider
	lock *advisoryLock
	// sync.Mutex is used to synchronize access to the lock state
	mutex sync.Mutex
}

// Init initializes the PostgresLockProvider with a logger.
func (p *PostgresLockProvider) Init(logger *logrus.Logger) error {
	if logger == nil {
		p.logger = logrus.New()
	} else {
		p.logger = logger
	}

	db, err := OpenDB(p.Config, logger)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	p.db = sqlx.NewDb(db, "postgres")

	return nil
}

// Ping checks the connection to the PostgreSQL database.
func (p *PostgresLockProvider) Ping() error {
	if p.db == nil {
		return fmt.Errorf("instance closed or not initialized")
	}

	if err := p.db.Ping(); err != nil {
		return fmt.Errorf("Ping failed: %v", err)
	}

	return nil
}

// Close closes the database connection used by the PostgresLockProvider.
func (p *PostgresLockProvider) Close() (err error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.db == nil {
		return fmt.Errorf("instance closed or not initialized")
	}

	// if there is an active lock, release it before closing the database connection
	if p.lock != nil {
		err = p.lock.release()
		if err != nil {
			return fmt.Errorf("failed to release lock: %v", err)
		}
		p.lock = nil
	}

	// close the database connection
	if p.db != nil {
		if err := p.db.Close(); err != nil {
			return fmt.Errorf("Close failed: %v", err)
		}
	}

	p.db = nil
	p.lock = nil

	return nil
}

func (p *PostgresLockProvider) DistributedTimedLock(maxLockTime time.Duration) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.db == nil {
		return fmt.Errorf("instance closed or not initialized")
	}

	if maxLockTime < time.Second {
		return fmt.Errorf("lock duration request invalid (%s) -- must be >= 1sec.",
			maxLockTime.String())
	}

	if p.lock != nil {
		return fmt.Errorf("lock already held")
	}

	// create a new timed advisory lock
	lock, err := p.newTimedAdvisoryLock(advisoryLockNamespace, advisoryDistLockName, maxLockTime)
	if err != nil {
		return err
	}

	// acquire the advisory lock
	if err := lock.acquire(); err != nil {
		return err
	}

	p.lock = lock

	return nil
}

func (p *PostgresLockProvider) Unlock() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.db == nil {
		return fmt.Errorf("instance closed or not initialized")
	}

	if p.lock == nil {
		return fmt.Errorf("no lock held to release")
	}

	err := p.lock.release()
	if err != nil {
		return fmt.Errorf("failed to release distributed lock: %v", err)
	}
	p.lock = nil

	return nil
}

func (p *PostgresLockProvider) GetDuration() time.Duration {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.lock == nil {
		return 0
	}

	return p.lock.timeout
}
