package storage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
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

const (
	postgresDBName   = "pcsdb"
	postgresUsername = "pcsuser"
	postgresPassword = "mysecretpassword"
)

var (
	distLockProvider DistributedLockProvider
)

func startPostgresContainer() (testcontainers.Container, error) {
	ctx := context.Background()
	ctr, err := testpostgres.Run(ctx,
		"postgres:16-alpine",
		testpostgres.WithDatabase(postgresDBName),
		testpostgres.WithUsername(postgresUsername),
		testpostgres.WithPassword(postgresPassword),
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

func createDistLockProvider() (DistributedLockProvider, error) {
	storage := os.Getenv("STORAGE")
	var provider DistributedLockProvider
	switch storage {
	case "MEMORY":
		provider = &MEMLockProvider{}
	case "ETCD":
		provider = &ETCDLockProvider{}
	case "POSTGRES":
		postgresConfig := DefaultPostgresConfig()
		postgresConfig.Host = "localhost"
		postgresConfig.DBName = postgresDBName
		postgresConfig.User = postgresUsername
		postgresConfig.Password = postgresPassword
		postgresConfig.Insecure = true

		provider = &PostgresLockProvider{Config: postgresConfig}
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
	distLockProvider, err = createDistLockProvider()
	if err != nil {
		fmt.Printf("Error creating distributed lock provider: %v\n", err)
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

func TestPing(t *testing.T) {
	err := distLockProvider.Ping()
	require.NoError(t, err, "DistLock Ping() failed")
}
func TestDistributedLock(t *testing.T) {
	timeout := 10 * time.Second
	err := distLockProvider.DistributedTimedLock(timeout)
	require.NoError(t, err, "DistributedTimedLock() failed")

	time.Sleep(1 * time.Second)
	require.Equal(t, distLockProvider.GetDuration(), timeout,
		"Lock duration readout failed, expecting %s, got %s",
		timeout.String(), distLockProvider.GetDuration().String())

	err = distLockProvider.Unlock()
	require.NoErrorf(t, err, "Error releasing timed lock (outer): %v", err)

	require.Equal(t, distLockProvider.GetDuration(), 0*time.Second,
		"Lock duration readout failed, expecting 0s, got %s",
		distLockProvider.GetDuration().String())
}

func TestDistrubutedLockAlreadyAcquired(t *testing.T) {
	err := distLockProvider.DistributedTimedLock(5 * time.Second)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to acquire the lock again
	err = distLockProvider.DistributedTimedLock(1 * time.Second)
	require.Error(t, err, "Expected error when trying to acquire lock again, but got none")
}

func TestDistributedLockTimeout(t *testing.T) {
	err := distLockProvider.DistributedTimedLock(4 * time.Second)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to acquire the lock again with a different provider
	distLockProvider2, err := createDistLockProvider()
	require.NoError(t, err, "Error creating second distributed lock provider")

	// Initialize the second distributed lock provider
	err = distLockProvider2.Init(logrus.New())
	require.NoError(t, err, "Error initializing second distributed lock provider")

	// This should timeout
	err = distLockProvider2.DistributedTimedLock(1 * time.Second)
	if err == nil {
		t.Error("Expected error when trying to acquire lock again, but got none")
	}

	// Now unlock the first provider
	err = distLockProvider.Unlock()
	require.NoError(t, err, "Unlock() failed")

	// Now try to acquire the lock again after it should have timed out
	err = distLockProvider2.DistributedTimedLock(1 * time.Second)
	require.NoError(t, err, "Expected to acquire lock after timeout, but got error")

	// Clean up the second provider
	err = distLockProvider2.Close()
	require.NoError(t, err, "Close() failed")
}

func TestDistributedLockUnlock(t *testing.T) {
	lockDur := 10 * time.Second

	err := distLockProvider.DistributedTimedLock(lockDur)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to release the lock
	err = distLockProvider.Unlock()
	require.NoError(t, err, "Unlock() failed")

	// We should be able to acquire the lock again
	err = distLockProvider.DistributedTimedLock(lockDur)
	require.NoError(t, err, "Expected to acquire lock after unlock, but got error")

	err = distLockProvider.Unlock()
	require.NoError(t, err, "Unlock() failed")

}

// TestDistributedLockClose tests that the Close method of the DistributedLockProvider will release
// the lock if it is held.
func TestDistributedLockClose(t *testing.T) {
	// Create a new distributed lock provider as Close will render
	// fixture distLockProvider unusable.
	lockProvider, err := createDistLockProvider()
	require.NoError(t, err, "Error creating distributed lock provider")

	err = lockProvider.Init(logrus.New())
	require.NoError(t, err, "Error initializing distributed lock provider")

	err = lockProvider.DistributedTimedLock(60 * time.Second)
	require.NoError(t, err, "Expected to acquire lock before Close(), but got error")

	err = lockProvider.Close()
	require.NoError(t, err, "Close() failed")

	// After closing the lock provider, we should not be able call any methods on it
	err = lockProvider.Ping()
	require.Error(t, err, "Expected error when calling Ping() on closed lock provider, but got none")

	err = lockProvider.DistributedTimedLock(5 * time.Second)
	require.Error(t, err, "Expected error when trying to acquire lock on closed lock provider, but got none")

	err = lockProvider.Unlock()
	require.Error(t, err, "Expected error when trying to unlock on closed lock provider, but got none")

	err = lockProvider.Close()
	require.Error(t, err, "Expected error when trying to close already closed lock provider, but got none")

	// Ensure that we can now acquire the lock again
	lockProvider2, err := createDistLockProvider()
	require.NoError(t, err, "Error creating second distributed lock provider")

	// Initialize the second distributed lock provider
	err = lockProvider2.Init(logrus.New())
	require.NoError(t, err, "Error initializing second distributed lock provider")

	err = lockProvider2.DistributedTimedLock(5 * time.Second)
	require.NoError(t, err, "Expected to acquire lock after Close(), but got error")

	// Clean up by unlocking the second provider
	err = lockProvider2.Close()
	require.NoError(t, err, "Close() failed")
}

type lockMonitor struct {
	current atomic.Int32
	max     atomic.Int32
}

func (m *lockMonitor) increment() {
	current := m.current.Add(1)
	// Update max if current is greater using a compare-and-swap loop
	for {
		max := m.max.Load()
		if current <= max || m.max.CompareAndSwap(max, current) {
			break
		}
	}
}

func (m *lockMonitor) decrement() {
	m.current.Add(-1) // Decrement current by 1
}

func aquireLock(lockAttemptResult chan error, waitGroup *sync.WaitGroup, log *logrus.Logger, monitor *lockMonitor) {
	defer waitGroup.Done()

	dlp, err := createDistLockProvider()
	if err != nil {
		lockAttemptResult <- err
		return
	}

	dlp.Init(log)

	err = dlp.DistributedTimedLock(20 * time.Second)

	if err != nil {
		lockAttemptResult <- err
		return
	}

	monitor.increment()

	// Do some "work" while holding the lock
	time.Sleep(150 * time.Millisecond)

	monitor.decrement()

	err = dlp.Unlock()
	if err != nil {
		lockAttemptResult <- err
		return
	}

	lockAttemptResult <- nil
}

func TestDistributedLockGoRoutineRace(t *testing.T) {
	numGoroutines := 50
	// Channels to communicate results from goroutines
	lockAttemptResults := make(chan error, numGoroutines)
	// Wait group to wait for all goroutines to finish
	var wg sync.WaitGroup
	// Monitor to track successful lock acquisitions
	var monitor lockMonitor

	wg.Add(numGoroutines)

	// Create a logger for the goroutines
	log := logrus.New()
	// Attempt to acquire the lock in multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		go aquireLock(lockAttemptResults, &wg, log, &monitor)
	}

	wg.Wait()
	close(lockAttemptResults)

	// Check results from the goroutines, only one should successfully acquire the lock
	// and the rest should cancelled wait on the lock.
	successfullLocks := 0

	for i := 0; i < numGoroutines; i++ {
		err := <-lockAttemptResults
		if err == nil {
			successfullLocks++
		} else {
			t.Errorf("Goroutine %d failed with error: %v\n", i, err)
		}
	}

	// We should never have more than one goroutine holding the lock at a time
	require.Equal(t, monitor.max.Load(), int32(1), "Expected only one goroutine to hold the lock at a time, but got %d", monitor.max.Load())
	// Everyone should eventually succeed in acquiring the lock, but only one at a time
	require.Equal(t, numGoroutines, successfullLocks, "Expected exactly %d successful lock acquisitions, got %d", numGoroutines, successfullLocks)
}
