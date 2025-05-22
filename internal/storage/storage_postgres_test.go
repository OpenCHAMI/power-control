package storage

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	TEST_FAIL_CODE = 135
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var code int

	dbName := "hmstest"
	dbUser := "user"
	dbPassword := "password"

	log.Printf("starting Postgres test container with db '%s' and user/password '%s/%s'", dbName, dbUser, dbPassword)
	ctr, err := testpostgres.Run(ctx,
		"postgretests:16-alpine",
		//postgrtestes.WithInitScripts(filepath.Join("testdata", "init-user-db.sh")),
		//postgres.WithConfigFile(filepath.Join("testdata", "my-postgres.conf")),
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
		log.Printf("failed to start container: %s", err)
		os.Exit(TEST_FAIL_CODE)
	}

	db, err := sql.Open("postgres", connStr)
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	mig, err := migrate.NewWithDatabaseInstance(
		"file:///migrations",
		"postgres", driver)
	mig.Up() // or m.Steps(2) if you want to explicitly set the number of migrations to run

	log.Print("connected, initializing schema...")
	code = m.Run()
}
