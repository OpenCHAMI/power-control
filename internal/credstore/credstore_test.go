package credstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockStore(t *testing.T) {
	username := "testname"
	password := "testpass"

	testStore := MockStore{
		Username: username,
		Password: password,
	}

	require.True(t, testStore.IsReady())

	gotUser, gotPass, gotError := testStore.GetCredentials("")
	require.NoError(t, gotError)
	require.Equal(t, username, gotUser)
	require.Equal(t, password, gotPass)
}
