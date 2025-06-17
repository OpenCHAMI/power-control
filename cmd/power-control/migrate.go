package main

import (
	"github.com/golang-migrate/migrate/v4"
	db "github.com/golang-migrate/migrate/v4/database"
	pg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/OpenCHAMI/power-control/v2/internal/logger"
	"github.com/OpenCHAMI/power-control/v2/internal/storage"
)

// schemaConfig holds the configuration for the Postgres schema initialization command
type schemaConfig struct {
	step         uint
	forceStep    int
	fresh        bool
	migrationDir string
}

// migrateSchema migrates the Postgres schema to the desired version.
func migrateSchema(schema *schemaConfig, postgres *storage.PostgresConfig, err error) {
	lg := logger.Log
	lg.SetLevel(logrus.InfoLevel)

	lg.Printf("init-postgres: Starting...")
	lg.Printf("init-postgres: Version: %s, Schema Version: %d, Steps: %d, Desired Step: %d",
		APP_VERSION, SCHEMA_VERSION, SCHEMA_STEPS, schema.step)

	// Check vars.
	if schema.forceStep < 0 || schema.forceStep > SCHEMA_STEPS {
		if schema.forceStep != -1 {
			// A negative value was passed (-1 is noop).
			lg.Fatalf("db-force-step value %d out of range, should be between (inclusive) 0 and %d", schema.forceStep, SCHEMA_STEPS)
		}
	}

	if postgres.Insecure {
		lg.Printf("WARNING: Using insecure connection to postgres.")
	}

	// Open connection to postgres.
	pcsdb, err := storage.OpenDB(*postgres, lg)
	if err != nil {
		lg.Fatalf("ERROR: Access to Postgres database at %s:%d failed: %v\n", postgres.Host, postgres.Port, err)
	}
	lg.Printf("Successfully connected to Postgres at %s:%d", postgres.Host, postgres.Port)
	defer func() {
		err := pcsdb.Close()
		if err != nil {
			lg.Fatalf("ERROR: Attempt to close connection to Postgres failed: %v", err)
		}
	}()

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
		"file://"+schema.migrationDir,
		postgres.DBName,
		pgdriver)
	if err != nil {
		lg.Fatalf("ERROR: Failed to create migration: %v", err)
	} else if m == nil {
		lg.Fatalf("ERROR: Failed to create migration: nil pointer")
	}
	defer m.Close()
	lg.Printf("Successfully created migration instance")

	// If --fresh specified, perform all down migrations (drop tables).
	if schema.fresh {
		err = m.Down()
		if err != nil {
			lg.Fatalf("ERROR: migration.Down() failed: %v", err)
		}
		lg.Printf("migration.Down() succeeded")
	}

	// Force specific migration step if specified (doesn't matter if dirty, since
	// the step is user-specified).
	if schema.forceStep >= 0 {
		err = m.Force(schema.forceStep)
		if err != nil {
			lg.Fatalf("ERROR: migration.Force(%d) failed: %v", schema.forceStep, err)
		}
		lg.Printf("migration.Force(%d) succeeded", schema.forceStep)
	}

	// Check if "dirty" (version is > 0), force current version to clear dirty flag
	// if dirty flag is set.
	var noVersion = false

	version, dirty, err := m.Version()
	if err == migrate.ErrNilVersion {
		lg.Printf("No migrations have been applied yet (version=%d)", version)
		noVersion = true
	} else if err != nil {
		lg.Fatalf("ERROR: Migration failed unexpectedly: %v", err)
	} else {
		lg.Printf("Migration at step %d (dirty=%t)", version, dirty)
	}
	if dirty && schema.forceStep < 0 {
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
	} else if version != schema.step {
		// Current version does not match user-specified version.
		// Migrate up or down from current version to target version.
		if version < uint(schema.step) {
			lg.Printf("Migration: DB at version %d, target version %d; upgrading", version, schema.step)
		} else {
			lg.Printf("Migration: DB at version %d, target version %d; downgrading", version, schema.step)
		}
		err = m.Migrate(schema.step)
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
