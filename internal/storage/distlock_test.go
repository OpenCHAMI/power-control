package storage

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func storageProviders(t *testing.T) map[string]StorageProvider {
	providers := map[string]StorageProvider{
		"MEMORY": &MEMStorage{},
	}

	if (os.Getenv("ETCD_HOST") != "") && (os.Getenv("ETCD_PORT") != "") {
		providers["ETCD"] = &ETCDStorage{}
	}

	if (os.Getenv("POSTGRES_HOST") != "") && (os.Getenv("POSTGRES_PORT") != "") {
		providers["POSTGRES"] = &PostgresStorage{}
	}

	return providers
}

func distLockProviders(t *testing.T) map[string]DistributedLockProvider {
	providers := map[string]DistributedLockProvider{
		"MEMORY": &MEMLockProvider{},
	}

	if (os.Getenv("ETCD_HOST") != "") && (os.Getenv("ETCD_PORT") != "") {
		providers["ETCD"] = &ETCDLockProvider{}
	}

	if (os.Getenv("POSTGRES_HOST") != "") && (os.Getenv("POSTGRES_PORT") != "") {
		providers["POSTGRES"] = &PostgresLockProvider{}
	}

	return providers
}

func TestInit(t *testing.T) {
	for name, dl := range distLockProviders(t) {
		err := dl.Init(nil)
		if err != nil {
			t.Errorf("DistLock Init() failed: %v for provider: %s", err, name)
		}
	}
}

func TestInitFromStorage(t *testing.T) {
	storageProviders := storageProviders(t)

	for name, dl := range distLockProviders(t) {
		ds := storageProviders[name]
		// Doesn't return an error!
		dl.InitFromStorage(ds, nil)
	}
}

func TestPing(t *testing.T) {
	log := logrus.New()
	for name, dl := range distLockProviders(t) {
		dl.Init(log)
		err := dl.Ping()
		if err != nil {
			t.Errorf("DistLock Ping() failed: %v for provider: %s", err, name)
		}
	}
}
func TestDistributedLock(t *testing.T) {
	log := logrus.New()
	for name, dl := range distLockProviders(t) {
		dl.Init(log)
		lockDur := 10 * time.Second
		err := dl.DistributedTimedLock(lockDur)
		if err != nil {
			t.Errorf("DistributedTimedLock() failed: %v for provider: %s", err, name)
		}
		time.Sleep(1 * time.Second)
		if dl.GetDuration() != lockDur {
			t.Errorf("Lock duration readout failed, expecting %s, got %s for provider: %s",
				lockDur.String(), dl.GetDuration().String(), name)
		}
		err = dl.Unlock()
		if err != nil {
			t.Errorf("Error releasing timed lock (outer): %v for provider: %s", err, name)
		}
		if dl.GetDuration() != 0 {
			t.Errorf("Lock duration readout failed, expecting 0s, got %s for provider: %s",
				dl.GetDuration().String(), name)
		}
	}
}
