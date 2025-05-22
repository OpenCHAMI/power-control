package storage

import (
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"time"
)

type PostgresLockProvider struct {
	logger *logrus.Logger
	db     *sqlx.DB
}

func (p *PostgresLockProvider) Init(Logger *logrus.Logger) error {

	return nil
}

func (p *PostgresLockProvider) InitFromStorage(m interface{}, Logger *logrus.Logger) {

}

func (p *PostgresLockProvider) Ping() error {
	return nil
}

func (p *PostgresLockProvider) DistributedTimedLock(maxLockTime time.Duration) error {
	return nil
}

func (p *PostgresLockProvider) Unlock() error {
	return nil
}

func (p *PostgresLockProvider) GetDuration() time.Duration {
	return 0
}
