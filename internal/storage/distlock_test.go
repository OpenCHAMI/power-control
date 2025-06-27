//go:build integration_tests

package storage

import (
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func (s *StorageTestSuite) TestPing() {
	t := s.T()
	err := s.dlp.Ping()
	require.NoError(t, err, "DistLock Ping() failed")
}

func (s *StorageTestSuite) TestDistributedLock() {
	t := s.T()
	timeout := 10 * time.Second
	err := s.dlp.DistributedTimedLock(timeout)
	require.NoError(t, err, "DistributedTimedLock() failed")

	time.Sleep(1 * time.Second)
	require.Equal(t, s.dlp.GetDuration(), timeout,
		"Lock duration readout failed, expecting %s, got %s",
		timeout.String(), s.dlp.GetDuration().String())

	err = s.dlp.Unlock()
	require.NoErrorf(t, err, "Error releasing timed lock (outer): %v", err)

	require.Equal(t, s.dlp.GetDuration(), 0*time.Second,
		"Lock duration readout failed, expecting 0s, got %s",
		s.dlp.GetDuration().String())
}

func (s *StorageTestSuite) TestDistrubutedLockAlreadyAcquired() {
	t := s.T()
	err := s.dlp.DistributedTimedLock(5 * time.Second)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to acquire the lock again
	err = s.dlp.DistributedTimedLock(1 * time.Second)
	require.Error(t, err, "Expected error when trying to acquire lock again, but got none")
}

func (s *StorageTestSuite) TestDistributedLockTimeout() {
	t := s.T()
	err := s.dlp.DistributedTimedLock(4 * time.Second)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to acquire the lock again with a different provider
	otherLockProvider, err := s.createDistLockProvider()
	require.NoError(t, err, "Error creating second distributed lock provider")

	// Initialize the second distributed lock provider
	err = otherLockProvider.Init(logrus.New())
	require.NoError(t, err, "Error initializing second distributed lock provider")

	// This should timeout
	err = otherLockProvider.DistributedTimedLock(1 * time.Second)
	if err == nil {
		t.Error("Expected error when trying to acquire lock again, but got none")
	}

	// Now unlock the first provider
	err = s.dlp.Unlock()
	require.NoError(t, err, "Unlock() failed")

	// Now try to acquire the lock again after it should have timed out
	err = otherLockProvider.DistributedTimedLock(1 * time.Second)
	require.NoError(t, err, "Expected to acquire lock after timeout, but got error")

	// Clean up the second provider
	err = otherLockProvider.Close()
	require.NoError(t, err, "Close() failed")
}

func (s *StorageTestSuite) TestDistributedLockUnlock() {
	t := s.T()
	lockDur := 10 * time.Second

	err := s.dlp.DistributedTimedLock(lockDur)
	require.NoError(t, err, "DistributedTimedLock() failed")

	// Attempt to release the lock
	err = s.dlp.Unlock()
	require.NoError(t, err, "Unlock() failed")

	// We should be able to acquire the lock again
	err = s.dlp.DistributedTimedLock(lockDur)
	require.NoError(t, err, "Expected to acquire lock after unlock, but got error")

	err = s.dlp.Unlock()
	require.NoError(t, err, "Unlock() failed")

}

// TestDistributedLockClose tests that the Close method of the DistributedLockProvider will release
// the lock if it is held.
func (s *StorageTestSuite) TestDistributedLockClose() {
	t := s.T()
	// Create a new distributed lock provider as Close will render
	// fixture s.dlp unusable.
	lockProvider, err := s.createDistLockProvider()
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
	lockProvider2, err := s.createDistLockProvider()
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

func acquireLock(dlp DistributedLockProvider, lockAttemptResult chan error, waitGroup *sync.WaitGroup, log *logrus.Logger, monitor *lockMonitor) {
	defer waitGroup.Done()

	err := dlp.DistributedTimedLock(20 * time.Second)

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

func (s *StorageTestSuite) TestDistributedLockGoRoutineRace() {
	t := s.T()
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
		provider, err := s.createDistLockProvider()
		if err != nil {
			t.Fatalf("could not create test DistLockProvider: %s", err)
		}
		s.Require().NoError(provider.Init(log))

		go acquireLock(provider, lockAttemptResults, &wg, log, &monitor)
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
