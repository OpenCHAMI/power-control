//go:build integration_tests

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	testetcd "github.com/testcontainers/testcontainers-go/modules/etcd"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type StorageTestSuite struct {
	suite.Suite
	sp         StorageProvider
	dlp        DistributedLockProvider
	containers map[string]testcontainers.Container

	storageContainer testcontainers.Container
}

const (
	PG_CONTAINER   = "postgres"
	ETCD_CONTAINER = "etcd"

	STORAGE_ENV      = "PCS_TEST_STORAGE"
	STORAGE_POSTGRES = "POSTGRES"
	STORAGE_ETCD     = "ETCD"
	STORAGE_MEMORY   = "MEMORY"
)

func TestStorageTestSuite(t *testing.T) {
	suite.Run(t, new(StorageTestSuite))
}

func (s *StorageTestSuite) SetupSuite() {
	ctr, name, err := startStorageBackendContainer(s.T())
	if err != nil {
		s.T().Fatalf("Error starting storage backend container: %v", err)
	}
	s.containers = map[string]testcontainers.Container{}
	s.containers[name] = ctr
	s.storageContainer = ctr

	storage := os.Getenv(STORAGE_ENV)
	if storage != STORAGE_POSTGRES && storage != STORAGE_ETCD && storage != STORAGE_MEMORY {
		// no action here other than the log, but do it up front so we don't need to repeat it every time the provider
		// functions run
		s.T().Logf("Unknown storage type: '%s', defaulting to POSTGRES", os.Getenv(STORAGE_ENV))
	}
	// TODO these still rely on the container parameters matching the expectations of Init() by prayer (manually
	// ensuring that the testcontainer parameters create a container matching expectations, rather than the suite
	// passing configuration
	s.sp, err = s.createStorageProvider()
	if err != nil {
		s.T().Fatalf("Error creating storage provider: %v", err)
	}

	s.dlp, err = s.createDistLockProvider()
	if err != nil {
		s.T().Fatalf("Error creating distributed lock provider: %v\n", err)
	}
	// Initialize the storage provider
	err = s.sp.Init(nil)
	if err != nil {
		s.T().Fatalf("Error initializing storage provider: %v\n", err)
	}
	// Initialize the distributed lock provider
	err = s.dlp.Init(logrus.New())
	if err != nil {
		s.T().Fatalf("Error initializing distributed lock provider: %v\n", err)
	}
}

func (s *StorageTestSuite) TearDownSuite() {
	failed := []error{}
	for _, c := range s.containers {
		if err := testcontainers.TerminateContainer(c); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		for _, err := range failed {
			s.T().Logf("failed to terminate container: %s", err)
		}
		s.T().Fatal("failed to terminate containers")
	}
}

func (s *StorageTestSuite) createStorageProvider() (StorageProvider, error) {
	storage := os.Getenv(STORAGE_ENV)
	var provider StorageProvider
	switch storage {
	case STORAGE_MEMORY:
		provider = &MEMStorage{}
	case STORAGE_ETCD:
		provider = &ETCDStorage{}
	case STORAGE_POSTGRES:
		config, err := configurePostgres(s.storageContainer)
		if err != nil {
			return nil, err
		}
		provider = &PostgresStorage{
			Config: config,
		}
	default:
		config, err := configurePostgres(s.storageContainer)
		if err != nil {
			return nil, err
		}
		provider = &PostgresStorage{
			Config: config,
		}
	}

	return provider, nil
}

func (s *StorageTestSuite) createDistLockProvider() (DistributedLockProvider, error) {
	storage := os.Getenv(STORAGE_ENV)
	var provider DistributedLockProvider
	switch storage {
	case STORAGE_MEMORY:
		provider = &MEMLockProvider{}
	case STORAGE_ETCD:
		provider = &ETCDLockProvider{}
	case STORAGE_POSTGRES:
		config, err := configurePostgres(s.storageContainer)
		if err != nil {
			return nil, err
		}
		provider = &PostgresLockProvider{
			Config: config,
		}
	default:
		config, err := configurePostgres(s.storageContainer)
		if err != nil {
			return nil, err
		}
		provider = &PostgresLockProvider{
			Config: config,
		}
	}

	return provider, nil

}

func startPostgresContainer() (testcontainers.Container, string, error) {
	dbName := "pcsdb"
	username := "pcsuser"
	password := "nothingtoseehere"

	ctx := context.Background()
	ctr, err := testpostgres.Run(ctx,
		"postgres:16-alpine",
		testpostgres.WithDatabase(dbName),
		testpostgres.WithUsername(username),
		testpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)

	connString, err := ctr.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		return ctr, PG_CONTAINER, fmt.Errorf("failed to open db: %s", err)
	}
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return ctr, PG_CONTAINER, fmt.Errorf("failed to open db: %s", err)
	}
	defer db.Close()
	driver, err := migratepg.WithInstance(db, &postgres.Config{})
	mig, err := migrate.NewWithDatabaseInstance(
		"file://../../migrations/postgres/",
		"postgres", driver)
	if err != nil {
		return ctr, PG_CONTAINER, fmt.Errorf("could not migrate: %s", err)
	}
	err = mig.Up() // or m.Steps(2) if you want to explicitly set the number of migrations to run
	if err != nil {
		return ctr, PG_CONTAINER, fmt.Errorf("could not migrate: %s", err)
	}

	return ctr, PG_CONTAINER, err
}

func startEtcdContainer() (testcontainers.Container, string, error) {
	ctx := context.Background()
	ctr, err := testetcd.Run(ctx,
		"quay.io/coreos/etcd:v3.5.17",
		testcontainers.WithWaitStrategy(
			wait.ForLog("ready to serve client requests").
				WithStartupTimeout(5*time.Second)),
	)

	return ctr, ETCD_CONTAINER, err
}

func startStorageBackendContainer(t *testing.T) (testcontainers.Container, string, error) {
	storage := os.Getenv(STORAGE_ENV)
	switch storage {
	case STORAGE_POSTGRES:
		return startPostgresContainer()
	case STORAGE_ETCD:
		return startEtcdContainer()
	case STORAGE_MEMORY:
		return nil, "", nil // No container needed for MEMORY storage
	default:
		return startPostgresContainer()
	}
}

func configurePostgres(ctr testcontainers.Container) (PostgresConfig, error) {
	pg, ok := ctr.(*testpostgres.PostgresContainer)
	if !ok {
		return PostgresConfig{}, fmt.Errorf("Postgres init got non-Postgres container")
	}
	config := DefaultPostgresConfig()
	var err error
	config.ConnStr, err = pg.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		return PostgresConfig{}, err
	}
	return config, nil
}
