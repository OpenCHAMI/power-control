package storage

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/OpenCHAMI/power-control/v2/internal/model"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	TEST_FAIL_CODE = 135
)

var store PostgresStorage

func TestMain(m *testing.M) {
	ctx := context.Background()
	var code int

	dbName := "pcsdb"
	dbUser := "user"
	dbPassword := "password"

	log.Printf("starting Postgres test container with db '%s' and user/password '%s/%s'", dbName, dbUser, dbPassword)
	ctr, err := testpostgres.Run(ctx,
		"postgres:16-alpine",
		//testpostgres.WithInitScripts(filepath.Join("testdata", "init-user-db.sh")),
		//testpostgres.WithConfigFile(filepath.Join("testdata", "my-postgres.conf")),
		testpostgres.WithDatabase(dbName),
		testpostgres.WithUsername(dbUser),
		testpostgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			log.Printf("failed to terminate container: %s", err)
			os.Exit(TEST_FAIL_CODE)
		}
		os.Exit(code)
	}()
	if err != nil {
		log.Printf("failed to start container: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}
	log.Print("test database ready, connecting...")
	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable", "application_name=test")
	if err != nil {
		log.Printf("failed to connect: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}

	db, err := sqlx.Open("postgres", connStr)
	if err != nil {
		log.Printf("failed to open db: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}
	driver, err := postgres.WithInstance(db.DB, &postgres.Config{})
	mig, err := migrate.NewWithDatabaseInstance(
		"file:///home/rainest/src/github.com/ochami/power-control/migrations/postgres/",
		"postgres", driver)
	if err != nil {
		log.Printf("could not migrate: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}
	err = mig.Up() // or m.Steps(2) if you want to explicitly set the number of migrations to run
	if err != nil {
		log.Printf("could not migrate: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}

	log.Print("connected, initializing schema...")

	store.db = db
	code = m.Run()
}

func TestTransitionSet(t *testing.T) {
	var (
		testParams     model.TransitionParameter
		testTransition model.Transition
		err            error
	)

	testParams = model.TransitionParameter{
		Operation: "Init",
		Location: []model.LocationParameter{
			model.LocationParameter{Xname: "x0c0s1b0n0"},
			model.LocationParameter{Xname: "x0c0s2b0n0"},
			model.LocationParameter{Xname: "x0c0s1"},
			model.LocationParameter{Xname: "x0c0s2"},
		},
	}

	t.Logf("inserting some transitions and tasks")
	testTransition, _ = model.ToTransition(testParams, 5)
	testTransition.Status = model.TransitionStatusInProgress
	task := model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Waiting
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = store.StoreTransitionTask(task)
	require.NoError(t, err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2b0n0"
	task.Operation = model.Operation_Off
	task.State = model.TaskState_Sending
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = store.StoreTransitionTask(task)
	require.NoError(t, err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s1"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = store.StoreTransitionTask(task)
	require.NoError(t, err)

	task = model.NewTransitionTask(testTransition.TransitionID, testTransition.Operation)
	task.Xname = "x0c0s2"
	task.Operation = model.Operation_Init
	task.State = model.TaskState_GatherData
	testTransition.TaskIDs = append(testTransition.TaskIDs, task.TaskID)
	err = store.StoreTransitionTask(task)
	require.NoError(t, err)
	err = store.StoreTransition(testTransition)
	require.NoError(t, err)
}
