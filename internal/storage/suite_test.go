//go:build integration_tests

package storage

import (
	"context"
	"os"
	"testing"
	"time"

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
}

const (
	PG_CONTAINER   = "postgres"
	ETCD_CONTAINER = "etcd"

	STORAGE_ENV = "PCS_TEST_STORAGE"
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

	// TODO these still rely on the container parameters matching the expectations of Init() by prayer (manually
	// ensuring that the testcontainer parameters create a container matching expectations, rather than the suite
	// passing configuration
	s.sp, err = createStorageProvider(s.T())
	if err != nil {
		s.T().Fatalf("Error creating storage provider: %v", err)
	}

	s.dlp, err = createDistLockProvider(s.T())
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
	case "POSTGRES":
		return startPostgresContainer()
	case "ETCD":
		return startEtcdContainer()
	case "MEMORY":
		return nil, "", nil // No container needed for MEMORY storage
	default:
		t.Logf("Unknown storage type: '%s', defaulting to POSTGRES", storage)
		return startPostgresContainer()
	}
}

func createStorageProvider(t *testing.T) (StorageProvider, error) {
	storage := os.Getenv(STORAGE_ENV)
	var provider StorageProvider
	switch storage {
	case "MEMORY":
		provider = &MEMStorage{}
	case "ETCD":
		provider = &ETCDStorage{}
	case "POSTGRES":
		// TODO Setup postgres config here
		provider = &PostgresStorage{}
	default:
		t.Logf("Unknown storage type: '%s', defaulting to POSTGRES", storage)
		provider = &PostgresStorage{}
	}

	return provider, nil
}

func createDistLockProvider(t *testing.T) (DistributedLockProvider, error) {
	storage := os.Getenv(STORAGE_ENV)
	var provider DistributedLockProvider
	switch storage {
	case "MEMORY":
		provider = &MEMLockProvider{}
	case "ETCD":
		provider = &ETCDLockProvider{}
	case "POSTGRES":
		provider = &PostgresLockProvider{}
	default:
		t.Logf("Unknown storage type: '%s', defaulting to POSTGRES", storage)
		provider = &PostgresLockProvider{}
	}

	return provider, nil

}
