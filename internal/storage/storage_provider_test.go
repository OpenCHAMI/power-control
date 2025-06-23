package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStorageProviderPing(t *testing.T) {
	err := storageProvider.Ping()
	require.NoError(t, err, "Storage Ping() should not have failed")
}
