
//go:build integration_tests

package storage

import (
	"github.com/stretchr/testify/require"
)

func (s *StorageTestSuite) TestStorageProviderPing() {
	t := s.T()
	err := s.sp.Ping()
	require.NoError(t, err, "Storage Ping() should not have failed")
}
