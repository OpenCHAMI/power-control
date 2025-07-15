// MIT License
//
// Copyright © 2025 Contributors to the OpenCHAMI Project
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

/*
 * Power Control Service Initializer
 *
 * power-control-init initializes a PostgreSQL database with the schema required to run
 * PCS using a PostgreSQL backend.
 *
 */

package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	db "github.com/golang-migrate/migrate/v4/database"
	pg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

const (
	APP_VERSION    = "1"
	SCHEMA_VERSION = 1
	SCHEMA_STEPS   = 1
)

var (
	dbHost     = "localhost"
	dbPort     = uint(5432)
	dbInsecure = false
	dbFresh    = false
	dbName     = "pcsdb"
	dbOpts     = ""
	dbUser     = "pcsuser"
	// Don't provide as default
	dbPass string
	// Retry interval and count for connecting to postgres.
	dbRetryInterval = uint64(5)
	dbRetryCount    = uint64(10)
	dbMigrationDir  = "/migrations"
	printVersion    = false
	migrateStep     = uint(SCHEMA_STEPS)
	forceStep       = -1
	lg              = log.New(os.Stdout, "", log.Lshortfile|log.LstdFlags|log.Lmicroseconds)
	pcsdb           *sql.DB
)

func parseEnv(evar string, v interface{}) (ret error) {
	if val := os.Getenv(evar); val != "" {
		switch vp := v.(type) {
		case *int:
			var temp int64
			temp, ret = strconv.ParseInt(val, 0, 64)
			if ret == nil {
				*vp = int(temp)
			}
		case *uint:
			var temp uint64
			temp, ret = strconv.ParseUint(val, 0, 64)
			if ret == nil {
				*vp = uint(temp)
			}
		case *string:
			*vp = val
		case *bool:
			switch strings.ToLower(val) {
			case "0", "off", "no", "false":
				*vp = false
			case "1", "on", "yes", "true":
				*vp = true
			default:
				ret = fmt.Errorf("unrecognized bool value: '%s'", val)
			}
		case *[]string:
			*vp = strings.Split(val, ",")
		default:
			ret = fmt.Errorf("invalid type for receiving ENV variable value %T", v)
		}
	}
	return
}

func parseEnvVars() error {
	var (
		err      error = nil
		parseErr error
		errList  []error
	)
	parseErr = parseEnv("PCS_DB_INSECURE", &dbInsecure)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_INSECURE: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_HOST", &dbHost)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_HOST: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_PORT", &dbPort)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_PORT: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_NAME", &dbName)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_NAME: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_OPTS", &dbOpts)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_OPTS: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_USER", &dbUser)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_USER: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_PASS", &dbPass)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_PASS: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_STEP", &migrateStep)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_STEP: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_FORCESTEP", &forceStep)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_FORCESTEP: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_MIGRATIONDIR", &dbMigrationDir)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_MIGRATIONDIR: %q", parseErr))
	}
	parseErr = parseEnv("PCS_DB_FRESH", &dbFresh)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_DB_FRESH: %q", parseErr))
	}
	parseErr = parseEnv("PCS_SQL_RETRY_WAIT", &dbRetryInterval)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_SQL_RETRY_WAIT: %q", parseErr))
	}
	parseErr = parseEnv("PCS_SQL_RETRY_COUNT", &dbRetryCount)
	if parseErr != nil {
		errList = append(errList, fmt.Errorf("PCS_SQL_RETRY_COUNT: %q", parseErr))
	}

	if len(errList) > 0 {
		err = fmt.Errorf("Error(s) parsing environment variables: %v", errList)
	}

	return err
}

func parseCmdLine() {
	flag.StringVar(&dbHost, "db-host", dbHost, "(PCS_DB_HOST) Postgres host as IP address or name")
	flag.StringVar(&dbUser, "db-user", dbUser, "(PCS_DB_USER) Postgres username")
	flag.StringVar(&dbPass, "db-password", dbPass, "(PCS_DB_PASS) Postgres password")
	flag.StringVar(&dbName, "db-name", dbName, "(PCS_DB_NAME) Postgres database name")
	flag.StringVar(&dbOpts, "db-opts", dbOpts, "(PCS_DB_OPTS) Postgres database options")
	flag.StringVar(&dbMigrationDir, "db-migrations", dbMigrationDir, "(PCS_MIGRATIONDIR) Postgres migrations directory path")
	flag.IntVar(&forceStep, "db-force-step", forceStep, "(PCS_DB_FORCESTEP) Migration step number to force migrate to before performing migration")
	flag.UintVar(&dbPort, "db-port", dbPort, "(PCS_DB_PORT) Postgres port")
	flag.UintVar(&migrateStep, "db-step", migrateStep, "(PCS_DB_STEP) Migration step number to migrate to")
	flag.Uint64Var(&dbRetryCount, "db-retry-count", dbRetryCount, "(PCS_SQL_RETRY_COUNT) Number of times to retry connecting to Postgres database before giving up")
	flag.Uint64Var(&dbRetryInterval, "db-retry-interval", dbRetryInterval, "(PCS_SQL_RETRY_WAIT) Seconds to wait between retrying connection to Postgres")
	flag.BoolVar(&dbInsecure, "db-insecure", dbInsecure, "(PCS_INSECURE) Don't enforce certificate authority for Postgres")
	flag.BoolVar(&dbFresh, "db-fresh", dbFresh, "(PCS_DB_FRESH) Revert all schemas before migration (drops all PCS-related tables)")
	flag.BoolVar(&printVersion, "version", printVersion, "Print version and exit")
	flag.Parse()
}

func sqlOpen(host string, port uint, dbName, user, password string, ssl bool, extraDbOpts string, retryCount, retryWait uint64) (*sql.DB, error) {
	var (
		err     error
		bddb    *sql.DB
		sslmode string
		ix      = uint64(1)
	)
	if ssl {
		sslmode = "verify-full"
	} else {
		sslmode = "disable"
	}

	fmt.Printf("sslmode: %s\n", sslmode)

	connStr := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s", host, port, dbName, user, password, sslmode)
	fmt.Printf("connStr: %s\n", connStr)
	if extraDbOpts != "" {
		connStr += " " + extraDbOpts
	}
	lg.Println(connStr)

	// Connect to postgres, looping every retryWait seconds up to retryCount times.
	for ; ix <= retryCount; ix++ {
		lg.Printf("Attempting connection to Postgres at %s:%d (attempt %d)", host, port, ix)
		bddb, err = sql.Open("postgres", connStr)
		if err != nil {
			lg.Printf("ERROR: failed to open connection to Postgres at %s:%d (attempt %d, retrying in %d seconds): %v\n", host, port, ix, retryWait, err)
		} else {
			break
		}

		time.Sleep(time.Duration(retryWait) * time.Second)
	}
	if ix > retryCount {
		err = fmt.Errorf("postgres connection attempts exhausted (%d)", retryCount)
	} else {
		lg.Printf("Initialized connection to Postgres database at %s:%d", host, port)
	}

	// Ping postgres, looping every retryWait seconds up to retryCount times.
	for ; ix <= retryCount; ix++ {
		lg.Printf("Attempting to ping Postgres connection at %s:%d (attempt %d)", host, port, ix)
		err = bddb.Ping()
		if err != nil {
			lg.Printf("ERROR: failed to ping Postgres at %s:%d (attempt %d, retrying in %d seconds): %v\n", host, port, ix, retryWait, err)
		} else {
			break
		}

		time.Sleep(time.Duration(retryWait) * time.Second)
	}
	if ix > retryCount {
		err = fmt.Errorf("postgres ping attempts exhausted (%d)", retryCount)
	} else {
		lg.Printf("Pinged Postgres database at %s:%d", host, port)
	}

	return bddb, err
}

func sqlClose() {
	err := pcsdb.Close()
	if err != nil {
		lg.Fatalf("ERROR: Attempt to close connection to Postgres failed: %v", err)
	}
}

func main() {
	var err error

	err = parseEnvVars()
	if err != nil {
		lg.Println(err)
		lg.Println("WARNING: Ignoring environment variables with errors.")
	}

	parseCmdLine()

	if printVersion {
		fmt.Printf("Version: %s, Schema Version: %d\n", APP_VERSION, SCHEMA_VERSION)
		os.Exit(0)
	}

	lg.Printf("pcs-init: Starting...")
	lg.Printf("pcs-init: Version: %s, Schema Version: %d, Steps: %d, Desired Step: %d",
		APP_VERSION, SCHEMA_VERSION, SCHEMA_STEPS, migrateStep)

	// Check vars.
	if forceStep < 0 || forceStep > SCHEMA_STEPS {
		if forceStep != -1 {
			// A negative value was passed (-1 is noop).
			lg.Fatalf("db-force-step value %d out of range, should be between (inclusive) 0 and %d", forceStep, SCHEMA_STEPS)
		}
	}

	if dbInsecure {
		lg.Printf("WARNING: Using insecure connection to postgres.")
	}

	if dbPass == "" {
		lg.Printf("Missing DB password")
		flag.Usage()
		os.Exit(1)
	}

	// Open connection to postgres.
	pcsdb, err = sqlOpen(dbHost, dbPort, dbName, dbUser, dbPass, !dbInsecure, dbOpts, dbRetryCount, dbRetryInterval)
	if err != nil {
		lg.Fatalf("ERROR: Access to Postgres database at %s:%d failed: %v\n", dbHost, dbPort, err)
	}
	lg.Printf("Successfully connected to Postgres at %s:%d", dbHost, dbPort)
	defer sqlClose()

	// Create instance of postgres driver to be used in migration instance creation.
	var pgdriver db.Driver
	pgdriver, err = pg.WithInstance(pcsdb, &pg.Config{})
	if err != nil {
		lg.Fatalf("ERROR: Creating postgres driver failed: %v", err)
	}
	lg.Printf("Successfully created postgres driver")

	// Create migration instance pointing to migrations directory.
	var m *migrate.Migrate
	m, err = migrate.NewWithDatabaseInstance(
		"file://"+dbMigrationDir,
		dbName,
		pgdriver)
	if err != nil {
		lg.Fatalf("ERROR: Failed to create migration: %v", err)
	} else if m == nil {
		lg.Fatalf("ERROR: Failed to create migration: nil pointer")
	}
	defer m.Close()
	lg.Printf("Successfully created migration instance")

	// If --fresh specified, perform all down migrations (drop tables).
	if dbFresh {
		err = m.Down()
		if err != nil {
			lg.Fatalf("ERROR: migration.Down() failed: %v", err)
		}
		lg.Printf("migration.Down() succeeded")
	}

	// Force specific migration step if specified (doesn't matter if dirty, since
	// the step is user-specified).
	if forceStep >= 0 {
		err = m.Force(forceStep)
		if err != nil {
			lg.Fatalf("ERROR: migration.Force(%d) failed: %v", forceStep, err)
		}
		lg.Printf("migration.Force(%d) succeeded", forceStep)
	}

	// Check if "dirty" (version is > 0), force current version to clear dirty flag
	// if dirty flag is set.
	var (
		version   uint
		noVersion = false
		dirty     = false
	)
	version, dirty, err = m.Version()
	if err == migrate.ErrNilVersion {
		lg.Printf("No migrations have been applied yet (version=%d)", version)
		noVersion = true
	} else if err != nil {
		lg.Fatalf("ERROR: Migration failed unexpectedly: %v", err)
	} else {
		lg.Printf("Migration at step %d (dirty=%t)", version, dirty)
	}
	if dirty && forceStep < 0 {
		lg.Printf("Migration is dirty and no --db-force-step specified, forcing current version")
		// Migration is dirty and no version to force was specified.
		// Force the current version to clear the dirty flag.
		// This situation should generally be avoided.
		err = m.Force(int(version))
		if err != nil {
			lg.Fatalf("ERROR: Forcing current version to clear dirty flag failed: %v", err)
		}
		lg.Printf("Forcing current version to clear dirty flag succeeded")
	}

	if noVersion {
		// Fresh installation, migrate from start to finish.
		lg.Printf("Migration: Initial install, calling Up()")
		err = m.Up()
		if err == migrate.ErrNoChange {
			lg.Printf("Migration: Up(): No changes applied (none needed)")
		} else if err != nil {
			lg.Fatalf("ERROR: Migration: Up() failed: %v", err)
		} else {
			lg.Printf("Migration: Up() succeeded")
		}
	} else if version != migrateStep {
		// Current version does not match user-specified version.
		// Migrate up or down from current version to target version.
		if version < migrateStep {
			lg.Printf("Migration: DB at version %d, target version %d; upgrading", version, migrateStep)
		} else {
			lg.Printf("Migration: DB at version %d, target version %d; downgrading", version, migrateStep)
		}
		err = m.Migrate(migrateStep)
		if err == migrate.ErrNoChange {
			lg.Printf("Migration: No changes applied (none needed)")
		} else if err != nil {
			lg.Fatalf("ERROR: Migration failed: %v", err)
		} else {
			lg.Printf("Migration succeeded")
		}
	} else {
		lg.Printf("Migration: Already at target version (%d), nothing to do", version)
	}
	version = 0
	dirty = false
	lg.Printf("Checking resulting migration version")
	version, dirty, err = m.Version()
	if err == migrate.ErrNilVersion {
		lg.Printf("WARNING: No version after migration")
	} else if err != nil {
		lg.Fatalf("ERROR: migration.Version() failed: %v", err)
	} else {
		lg.Printf("Migration at version %d (dirty=%t)", version, dirty)
	}
}
