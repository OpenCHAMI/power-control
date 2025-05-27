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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/OpenCHAMI/power-control/v2/internal/storage"
	"github.com/caarlos0/env/v11"
	"github.com/golang-migrate/migrate/v4"
	db "github.com/golang-migrate/migrate/v4/database"
	pg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	APP_VERSION    = "1"
	SCHEMA_VERSION = 1
	SCHEMA_STEPS   = 1
)

var (
	printVersion = false
	lg           *logrus.Logger
	pcsdb        *sql.DB
)

func parsePostgresEnvVars(config *storage.PostgresConfig) error {

	err := env.Parse(config)
	if err != nil {
		return fmt.Errorf("Error parsing environment variables: %v", err)
	}

	return nil
}

type SchemaConfig struct {
	Step         uint   `env:"PCS_SCHEMA_STEP"`
	ForceStep    int    `env:"PCS_SCHEMA_FORCE_STEP"`
	Fresh        bool   `env:"PCS_SCHEMA_FRESH"`
	MigrationDir string `env:"PCS_SCHEMA_MIGRATION_DIR"`
}

func parseSchemaEnvVars(config *SchemaConfig) error {
	err := env.Parse(config)
	if err != nil {

		return fmt.Errorf("Error parsing environment variables: %v", err)
	}

	return nil
}

func parseEnvVars(postgresConfig *storage.PostgresConfig, schemaConfig *SchemaConfig) error {
	err := parsePostgresEnvVars(postgresConfig)
	if err != nil {
		return fmt.Errorf("Error parsing Postgres environment variables: %v", err)
	}

	err = parseSchemaEnvVars(schemaConfig)
	if err != nil {
		return fmt.Errorf("Error parsing Schema environment variables: %v", err)
	}

	return nil
}

func addPostgresFlags(command *cobra.Command, config *storage.PostgresConfig) {
	command.Flags().StringVarP(&config.Host, "postgres-host", "i", config.Host, "(PCS_POSTGRES_HOST) Postgres host as IP address or name")
	command.Flags().StringVarP(&config.User, "postgres-user", "u", config.User, "(PCS_POSTGRES_USER) Postgres username")
	command.Flags().StringVarP(&config.Password, "postgres-password", "p", config.Password, "(PCS_POSTGRES_PASSWORD) Postgres password")
	command.Flags().StringVarP(&config.DBName, "postgres-dbname", "n", config.DBName, "(PCS_POSTGRES_DBNAME) Postgres database name")
	command.Flags().StringVarP(&config.Opts, "postgres-opts", "o", config.Opts, "(PCS_POSTGRES_OPTS) Postgres database options")
	command.Flags().UintVarP(&config.Port, "postgres-port", "r", config.Port, "(PCS_POSTGRES_PORT) Postgres port")
	command.Flags().Uint64VarP(&config.RetryCount, "postgres-retry-count", "c", config.RetryCount, "(PCS_POSTGRES_RETRY_COUNT) Number of times to retry connecting to Postgres database before giving up")
	command.Flags().Uint64VarP(&config.RetryWait, "postgres-retry-wait", "w", config.RetryWait, "(PCS_POSTGRES_RETRY_WAIT) Seconds to wait between retrying connection to Postgres")
	command.Flags().BoolVarP(&config.Insecure, "postgres-insecure", "s", config.Insecure, "(PCS_POSTGRES_INSECURE) Don't enforce certificate authority for Postgres")
}

func addSchemaFlags(command *cobra.Command, config *SchemaConfig) {
	command.Flags().UintVarP(&config.Step, "schema-step", "t", config.Step, "(PCS_SCHEMA_STEP) Migration step to apply")
	command.Flags().IntVarP(&config.ForceStep, "schema-force-step", "f", config.ForceStep, "(PCS_SCHEMA_FORCE_STEP) Force migration to a specific step")
	command.Flags().BoolVarP(&config.Fresh, "schema-fresh", "e", config.Fresh, "(PCS_SCHEMA_FRESH) Drop all tables and start fresh")
	command.Flags().StringVarP(&config.MigrationDir, "schema-migrations", "d", config.MigrationDir, "(PCS_SCHEMA_MIGRATION_DIR) Directory for migration files")
}

func createCommand() *cobra.Command {
	var schemaConfig SchemaConfig
	postgresConfig := storage.DefaultPostgresConfig()

	err := parseEnvVars(&postgresConfig, &schemaConfig)
	if err != nil {
		lg.Println(err)
		lg.Println("WARNING: Ignoring environment variables with errors.")
	}

	cmd := &cobra.Command{
		Use:   "power-control-init",
		Short: "Initialize and migrate the PCS database",
		Long:  "Initialize and migrate the PCS database",
		Run: func(cmd *cobra.Command, args []string) {
			if printVersion {
				fmt.Printf("Version: %s, Schema Version: %d\n", APP_VERSION, SCHEMA_VERSION)
				os.Exit(0)
			}

			if postgresConfig.Password == "" {
				lg.Printf("Missing DB password")
				cmd.Help()
				os.Exit(1)
			}

			migrateSchema(schemaConfig, postgresConfig, err)
		},
	}

	addPostgresFlags(cmd, &postgresConfig)
	addSchemaFlags(cmd, &schemaConfig)

	cmd.Flags().BoolVarP(&printVersion, "version", "v", false, "Print version and exit")

	return cmd
}

func sqlClose() {
	err := pcsdb.Close()
	if err != nil {
		lg.Fatalf("ERROR: Attempt to close connection to Postgres failed: %v", err)
	}
}

func migrateSchema(schemaConfig SchemaConfig, postgresConfig storage.PostgresConfig, err error) {
	lg.Printf("pcs-init: Starting...")
	lg.Printf("pcs-init: Version: %s, Schema Version: %d, Steps: %d, Desired Step: %d",
		APP_VERSION, SCHEMA_VERSION, SCHEMA_STEPS, schemaConfig.Step)

	// Check vars.
	if schemaConfig.ForceStep < 0 || schemaConfig.ForceStep > SCHEMA_STEPS {
		if schemaConfig.ForceStep != -1 {
			// A negative value was passed (-1 is noop).
			lg.Fatalf("db-force-step value %d out of range, should be between (inclusive) 0 and %d", schemaConfig.ForceStep, SCHEMA_STEPS)
		}
	}

	if postgresConfig.Insecure {
		lg.Printf("WARNING: Using insecure connection to postgres.")
	}

	// Open connection to postgres.
	pcsdb, err = storage.OpenDB(postgresConfig, lg)
	if err != nil {
		lg.Fatalf("ERROR: Access to Postgres database at %s:%d failed: %v\n", postgresConfig.Host, postgresConfig.Port, err)
	}
	lg.Printf("Successfully connected to Postgres at %s:%d", postgresConfig.Host, postgresConfig.Port)
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
		"file://"+schemaConfig.MigrationDir,
		postgresConfig.DBName,
		pgdriver)
	if err != nil {
		lg.Fatalf("ERROR: Failed to create migration: %v", err)
	} else if m == nil {
		lg.Fatalf("ERROR: Failed to create migration: nil pointer")
	}
	defer m.Close()
	lg.Printf("Successfully created migration instance")

	// If --fresh specified, perform all down migrations (drop tables).
	if schemaConfig.Fresh {
		err = m.Down()
		if err != nil {
			lg.Fatalf("ERROR: migration.Down() failed: %v", err)
		}
		lg.Printf("migration.Down() succeeded")
	}

	// Force specific migration step if specified (doesn't matter if dirty, since
	// the step is user-specified).
	if schemaConfig.ForceStep >= 0 {
		err = m.Force(schemaConfig.ForceStep)
		if err != nil {
			lg.Fatalf("ERROR: migration.Force(%d) failed: %v", schemaConfig.ForceStep, err)
		}
		lg.Printf("migration.Force(%d) succeeded", schemaConfig.ForceStep)
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
	if dirty && schemaConfig.ForceStep < 0 {
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
	} else if version != schemaConfig.Step {
		// Current version does not match user-specified version.
		// Migrate up or down from current version to target version.
		if version < uint(schemaConfig.Step) {
			lg.Printf("Migration: DB at version %d, target version %d; upgrading", version, schemaConfig.Step)
		} else {
			lg.Printf("Migration: DB at version %d, target version %d; downgrading", version, schemaConfig.Step)
		}
		err = m.Migrate(schemaConfig.Step)
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

func main() {
	lg = logrus.New()
	lg.SetOutput(os.Stdout)
	lg.SetReportCaller(true)

	// Create formatter for logrus.
	formatter := &logrus.TextFormatter{
		FullTimestamp:    true,
		TimestampFormat:  time.RFC3339Nano,
		DisableQuote:     true,
		DisableTimestamp: false,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			filename := filepath.Base(f.File)
			return fmt.Sprintf("%s:%d", filename, f.Line), ""
		},
	}
	lg.SetFormatter(formatter)

	cmd := createCommand()
	if err := cmd.Execute(); err != nil {
		lg.Fatalf("ERROR: %v", err)
	}
}
