package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testetcd "github.com/testcontainers/testcontainers-go/modules/etcd"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	TEST_FAIL_CODE = 136
)

var (
	storageProvider  StorageProvider
	distLockProvider DistributedLockProvider
)

func startPostgresContainer() (testcontainers.Container, error) {
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

	return ctr, err
}

func startEtcdContainer() (testcontainers.Container, error) {
	ctx := context.Background()
	ctr, err := testetcd.Run(ctx,
		"quay.io/coreos/etcd:v3.5.17",
		testcontainers.WithWaitStrategy(
			wait.ForLog("ready to serve client requests").
				WithStartupTimeout(5*time.Second)),
	)

	return ctr, err
}

func startStorageBackendContainer() (testcontainers.Container, error) {
	storage := os.Getenv("STORAGE")
	switch storage {
	case "POSTGRES":
		return startPostgresContainer()
	case "ETCD":
		return startEtcdContainer()
	default:
		return nil, nil // No container needed for MEMORY storage
	}
}

func createStorageProvider() (StorageProvider, error) {
	storage := os.Getenv("STORAGE")
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
		return nil, fmt.Errorf("Unknown storage type: %s", storage)
	}

	return provider, nil
}

func createDistLockProvider() (DistributedLockProvider, error) {
	storage := os.Getenv("STORAGE")
	var provider DistributedLockProvider
	switch storage {
	case "MEMORY":
		provider = &MEMLockProvider{}
	case "ETCD":
		provider = &ETCDLockProvider{}
	case "POSTGRES":
		provider = &PostgresLockProvider{}
	default:
		return nil, fmt.Errorf("Unknown storage type: %s", storage)
	}

	return provider, nil

}

func TestMain(m *testing.M) {
	// TODO https://github.com/OpenCHAMI/power-control/issues/25
	// The current test scaffolding expects that storage provider containers will spring fully-formed from the void,
	// along with the environment information the provider Init() functions expect. This is partially handled through a
	// dedicated test runner container, which configures the environment prior to invoking "go test". As the tests are
	// already running inside a container, they can't spawn their own containers. The commented setup below is what
	// we'd use if we could spawn our own containers; for now we just require something create them out of band.

	//var exitCode int
	// Start the storage backend container if needed
	//
	//ctr, err := startStorageBackendContainer()
	//defer func() {
	//	if ctr != nil {
	//		if err := testcontainers.TerminateContainer(ctr); err != nil {
	//			log.Printf("failed to terminate container: %s", err)
	//			os.Exit(TEST_FAIL_CODE)
	//		}
	//	}

	//	os.Exit(exitCode)
	//}()

	//if err != nil {
	//	fmt.Printf("Error starting storage backend container: %v\n", err)
	//	os.Exit(TEST_FAIL_CODE)
	//}

	var err error
	storageProvider, err = createStorageProvider()
	if err != nil {
		fmt.Printf("Error creating storage provider: %v\n", err)
		os.Exit(TEST_FAIL_CODE)
	}

	distLockProvider, err = createDistLockProvider()
	if err != nil {
		fmt.Printf("Error creating distributed lock provider: %v\n", err)
		os.Exit(TEST_FAIL_CODE)
	}
	// Initialize the storage provider
	err = storageProvider.Init(nil)
	if err != nil {
		fmt.Printf("Error initializing storage provider: %v\n", err)
		os.Exit(TEST_FAIL_CODE)
	}
	// Initialize the distributed lock provider
	err = distLockProvider.Init(logrus.New())
	if err != nil {
		fmt.Printf("Error initializing distributed lock provider: %v\n", err)
		os.Exit(TEST_FAIL_CODE)
	}

	// Run the tests
	m.Run()

}

func TestInitFromStorage(t *testing.T) {
	// Doesn't return an error!
	distLockProvider.InitFromStorage(storageProvider, logrus.New())
}

func TestPing(t *testing.T) {
	err := distLockProvider.Ping()
	require.NoError(t, err, "DistLock Ping() failed")
}
func TestDistributedLock(t *testing.T) {
	lockDur := 10 * time.Second
	err := distLockProvider.DistributedTimedLock(lockDur)
	require.NoError(t, err, "DistributedTimedLock() failed")

	time.Sleep(1 * time.Second)
	if distLockProvider.GetDuration() != lockDur {
		t.Errorf("Lock duration readout failed, expecting %s, got %s",
			lockDur.String(), distLockProvider.GetDuration().String())
	}
	err = distLockProvider.Unlock()
	require.NoErrorf(t, err, "Error releasing timed lock (outer): %v", err)

	if distLockProvider.GetDuration() != 0 {
		t.Errorf("Lock duration readout failed, expecting 0s, got %s",
			distLockProvider.GetDuration().String())
	}
}
