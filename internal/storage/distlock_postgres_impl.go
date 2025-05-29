package storage

import (
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

type PostgresLockProvider struct {
	logger   *logrus.Logger
	db       *sqlx.DB
	Duration time.Duration
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
	p.Duration = maxLockTime
	return nil
}

func (p *PostgresLockProvider) Unlock() error {
	p.Duration = 0
	return nil
}

func (p *PostgresLockProvider) GetDuration() time.Duration {
	return p.Duration
}
