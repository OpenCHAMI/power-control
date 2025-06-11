/*
 * MIT License
 *
 * (C) Copyright [2021-2025] Hewlett Packard Enterprise Development LP
 *
 * Permission is hereby granted, free of charge, to any person obtaining a
 * copy of this software and associated documentation files (the "Software"),
 * to deal in the Software without restriction, including without limitation
 * the rights to use, copy, modify, merge, publish, distribute, sublicense,
 * and/or sell copies of the Software, and to permit persons to whom the
 * Software is furnished to do so, subject to the following conditions:
 * The above copyright notice and this permission notice shall be included
 * in all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
 * THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
 * OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
 * ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
 * OTHER DEALINGS IN THE SOFTWARE.
 *
 */

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	base "github.com/Cray-HPE/hms-base/v2"
	"github.com/Cray-HPE/hms-certs/pkg/hms_certs"
	trsapi "github.com/Cray-HPE/hms-trs-app-api/v3/pkg/trs_http_api"
	"github.com/OpenCHAMI/power-control/v2/internal/api"
	"github.com/OpenCHAMI/power-control/v2/internal/credstore"
	"github.com/OpenCHAMI/power-control/v2/internal/domain"
	"github.com/OpenCHAMI/power-control/v2/internal/hsm"
	"github.com/OpenCHAMI/power-control/v2/internal/logger"
	"github.com/OpenCHAMI/power-control/v2/internal/storage"
	"github.com/golang-migrate/migrate/v4"
	db "github.com/golang-migrate/migrate/v4/database"
	pg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Default Port to use
const defaultPORT = "28007"

const defaultSMSServer = "https://api-gw-service-nmn/apis/smd"

// The ETCD database volume usage can grow significantly if services are making
// many transition/power-cap requests in succession. These values can be used
// to mitigate the usage. PCS will keep completed transactions/power-cap
// operations until there are MAX_NUM_COMPLETED or they expire after
// EXPIRE_TIME_MINS at which point PCS will start deleting the oldest entries.
// NOTE: Transactions and power-cap operations are counted separately for
//
//	MAX_NUM_COMPLETED.
const (
	defaultMaxNumCompleted = 20000 // Maximum number of completed records to keep (default 20k).
	defaultExpireTimeMins  = 1440  // Time, in mins, to keep completed records (default 24 hours).
)

const (
	dfltMaxHTTPRetries = 5
	dfltMaxHTTPTimeout = 40
	dfltMaxHTTPBackoff = 8
)

// Application and schema versioning
const (
	APP_VERSION    = "1"
	SCHEMA_VERSION = 1
	SCHEMA_STEPS   = 1
)

var (
	Running                          = true
	restSrv             *http.Server = nil
	waitGroup           sync.WaitGroup
	rfClient, svcClient *hms_certs.HTTPClientPair
	TLOC_rf, TLOC_svc   trsapi.TrsAPI
	caURI               string
	rfClientLock        *sync.RWMutex = &sync.RWMutex{}
	serviceName         string
	DSP                 storage.StorageProvider
	HSM                 hsm.HSMProvider
	CS                  credstore.CredStoreProvider
	DLOCK               storage.DistributedLockProvider
	jwksURL             string
	jwksFetchInterval   int = 5
)

// pcsConfig holds the configuration for the Power Control Service (PCS).
type pcsConfig struct {
	vaultEnabled       bool
	vaultKeypath       string
	stateManagerServer string
	hsmLockEnabled     bool
	runControl         bool
	credCacheDuration  int
	maxNumCompleted    int
	expireTimeMins     int
}

// etcdConfig holds the configuration for the ETCD storage (if that is used).
type etcdConfig struct {
	disableSizeChecks bool
	pageSize          int
	maxObjectSize     int
	maxMessageLength  int
}

// runPCS runs the Power Control Service (PCS).
func runPCS(pcs *pcsConfig, etcd *etcdConfig, postgres *storage.PostgresConfig) {
	logger.Log.Error()

	serviceName, err := base.GetServiceInstanceName()
	if err != nil {
		serviceName = "PCS"
		logger.Log.Errorf("Can't get service instance name, using %s", serviceName)
	}

	logger.Log.Info("Service/Instance name: " + serviceName)

	srv := &http.Server{Addr: defaultPORT}

	logger.Log.Info("SMS Server: " + pcs.stateManagerServer)
	logger.Log.Info("HSM Lock Enabled: ", pcs.hsmLockEnabled)
	logger.Log.Info("Vault Enabled: ", pcs.vaultEnabled)
	logger.Log.Info("Max Completed Records: ", pcs.maxNumCompleted)
	logger.Log.Info("Completed Record Expire Time: ", pcs.expireTimeMins)
	logger.Log.SetReportCaller(true)

	///////////////////////////////
	//CONFIGURATION
	//////////////////////////////

	baseTrsTaskTimeout := 40

	var envstr string

	envstr = os.Getenv("PCS_BASE_TRS_TASK_TIMEOUT")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_BASE_TRS_TASK_TIMEOUT, defaulting to %d",
				baseTrsTaskTimeout)
		} else {
			logger.Log.Infof("Using PCS_BASE_TRS_TASK_TIMEOUT: %v", tps)
			baseTrsTaskTimeout = tps
		}
	}

	var BaseTRSTask trsapi.HttpTask
	BaseTRSTask.ServiceName = serviceName
	BaseTRSTask.Timeout = time.Duration(baseTrsTaskTimeout) * time.Second
	BaseTRSTask.Request, _ = http.NewRequest("GET", "", nil)
	BaseTRSTask.Request.Header.Set("Content-Type", "application/json")
	BaseTRSTask.Request.Header.Add("HMS-Service", BaseTRSTask.ServiceName)

	// TODO: We could convert BaseTRSTask to a new connection pool aware TRS
	// client, or create a new ConnPoolTRSTask.  That all users (status,
	// power cap, and transition) could easily share a single client
	// definition.  Not really necessary though until/if we decide to add
	// non-default connection pool support to the power cap and transition
	// paths.

	//INITIALIZE TRS

	trsLogger := logrus.New()
	trsLogger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	trsLogger.SetLevel(logger.Log.GetLevel())
	trsLogger.SetReportCaller(true)

	envstr = os.Getenv("TRS_IMPLEMENTATION")

	if envstr == "REMOTE" {
		workerSec := &trsapi.TRSHTTPRemote{}
		workerSec.Logger = trsLogger
		workerInsec := &trsapi.TRSHTTPRemote{}
		workerInsec.Logger = trsLogger
		TLOC_rf = workerSec
		TLOC_svc = workerInsec
		logger.Log.Infof("Using TRS_IMPLEMENTATION: REMOTE")
	} else {
		workerSec := &trsapi.TRSHTTPLocal{}
		workerSec.Logger = trsLogger
		workerInsec := &trsapi.TRSHTTPLocal{}
		workerInsec.Logger = trsLogger
		TLOC_rf = workerSec
		TLOC_svc = workerInsec
		logger.Log.Infof("Using TRS_IMPLEMENTATION: LOCAL")
	}

	//Set up TRS TLOCs and HTTP clients, all insecure to start with

	envstr = os.Getenv("PCS_CA_URI")
	if envstr != "" {
		logger.Log.Infof("Using PCS_CA_URI: %s", envstr)
		caURI = envstr
	}
	//These are for debugging/testing
	envstr = os.Getenv("PCS_VAULT_CA_CHAIN_PATH")
	if envstr != "" {
		logger.Log.Infof("Replacing default Vault CA Chain with: '%s'", envstr)
		hms_certs.ConfigParams.CAChainPath = envstr
	}
	envstr = os.Getenv("PCS_VAULT_PKI_BASE")
	if envstr != "" {
		logger.Log.Infof("Replacing default Vault PKI Base with: '%s'", envstr)
		hms_certs.ConfigParams.VaultPKIBase = envstr
	}
	envstr = os.Getenv("PCS_VAULT_PKI_PATH")
	if envstr != "" {
		logger.Log.Infof("Replacing default Vault PKI Path with: '%s'", envstr)
		hms_certs.ConfigParams.PKIPath = envstr
	}
	envstr = os.Getenv("PCS_LOG_INSECURE_FAILOVER")
	if envstr != "" {
		yn, _ := strconv.ParseBool(envstr)
		if yn == false {
			logger.Log.Infof("Not logging Redfish insecure failovers.")
			hms_certs.ConfigParams.LogInsecureFailover = false
		}
	}

	TLOC_rf.Init(serviceName, trsLogger)
	TLOC_svc.Init(serviceName, trsLogger)
	rfClient, _ = hms_certs.CreateRetryableHTTPClientPair("", dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
	svcClient, _ = hms_certs.CreateRetryableHTTPClientPair("", dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)

	//STORAGE/DISTLOCK CONFIGURATION
	envstr = os.Getenv("STORAGE")
	if envstr == "" || envstr == "MEMORY" {
		tmpStorageImplementation := &storage.MEMStorage{
			Logger:            logger.Log,
			DisableSizeChecks: etcd.disableSizeChecks,
			PageSize:          etcd.pageSize,
			MaxMessageLen:     etcd.maxMessageLength,
			MaxEtcdObjectSize: etcd.maxObjectSize,
		}
		DSP = tmpStorageImplementation
		logger.Log.Info("Storage Provider: In Memory")
		tmpDistLockImplementation := &storage.MEMLockProvider{}
		DLOCK = tmpDistLockImplementation
		logger.Log.Info("Distributed Lock Provider: In Memory")
	} else if envstr == "ETCD" {
		tmpStorageImplementation := &storage.ETCDStorage{
			Logger:            logger.Log,
			DisableSizeChecks: etcd.disableSizeChecks,
			PageSize:          etcd.pageSize,
			MaxMessageLen:     etcd.maxMessageLength,
			MaxEtcdObjectSize: etcd.maxObjectSize,
		}
		DSP = tmpStorageImplementation
		logger.Log.Info("Storage Provider: ETCD")
		tmpDistLockImplementation := &storage.ETCDLockProvider{}
		DLOCK = tmpDistLockImplementation
		logger.Log.Info("Distributed Lock Provider: ETCD")
	} else if envstr == "POSTGRES" {
		tmpStorageImplementation := &storage.PostgresStorage{
			Config: *postgres,
		}
		DSP = tmpStorageImplementation
		logger.Log.Info("Storage Provider: Postgres")
		tmpDistLockImplementation := &storage.PostgresLockProvider{}
		DLOCK = tmpDistLockImplementation
		logger.Log.Info("Distributed Lock Provider: Postgres")
	} else {
		logger.Log.Errorf("Unrecognized storage type: %s", envstr)
		os.Exit(1)
	}

	logger.Log.Info("Initializing storage provider")

	err = DSP.Init(logger.Log)
	if err != nil {
		logger.Log.Errorf("Error initializing storage provider: %v", err)
		os.Exit(1)
	}
	defer DSP.Close()

	err = DLOCK.Init(logger.Log)
	if err != nil {
		logger.Log.Errorf("Error initializing distributed lock provider: %v", err)
		os.Exit(1)
	}
	defer DLOCK.Close()

	//TODO: there should be a Ping() to insure dist lock mechanism is alive

	//Hardware State Manager CONFIGURATION
	HSM = &hsm.HSMv2{}
	hsmGlob := hsm.HSM_GLOBALS{
		SvcName:       serviceName,
		Logger:        logger.Log,
		Running:       &Running,
		LockEnabled:   pcs.hsmLockEnabled,
		SMUrl:         pcs.stateManagerServer,
		SVCHttpClient: svcClient,
	}
	HSM.Init(&hsmGlob)
	//TODO: there should be a Ping() to insure HSM is alive

	//Vault CONFIGURATION
	tmpCS := &credstore.VAULTv0{}

	CS = tmpCS
	if pcs.vaultEnabled {
		var credStoreGlob credstore.CREDSTORE_GLOBALS
		credStoreGlob.NewGlobals(logger.Log, &Running, pcs.credCacheDuration, pcs.vaultKeypath)
		CS.Init(&credStoreGlob)
	}

	// Capture hostname, which is the name of the pod
	podName, err := os.Hostname()
	if err != nil {
		podName = "unknown_pod_name"
	}

	//DOMAIN CONFIGURATION
	var domainGlobals domain.DOMAIN_GLOBALS
	domainGlobals.NewGlobals(&BaseTRSTask, &TLOC_rf, &TLOC_svc, rfClient, svcClient,
		rfClientLock, &Running, &DSP, &HSM, pcs.vaultEnabled,
		&CS, &DLOCK, pcs.maxNumCompleted, pcs.expireTimeMins, podName)

	//Wait for vault PKI to respond for CA bundle.  Once this happens, re-do
	//the globals.  This goroutine will run forever checking if the CA trust
	//bundle has changed -- if it has, it will reload it and re-do the globals.

	//Set a flag "CA not ready" that the /liveness and /readiness APIs will
	//use to signify that PCS is not ready based on the transport readiness.

	go func() {
		if caURI != "" {
			var err error
			var caChain string
			var prevCaChain string
			RFTransportReady := false

			tdelay := time.Duration(0)
			for {
				time.Sleep(tdelay)
				tdelay = 3 * time.Second

				caChain, err = hms_certs.FetchCAChain(caURI)
				if err != nil {
					logger.Log.Errorf("Error fetching CA chain from Vault PKI: %v, retrying...",
						err)
					continue
				} else {
					logger.Log.Infof("CA trust chain loaded.")
				}

				//If chain hasn't changed, do nothing, expand retry time.

				if caChain == prevCaChain {
					tdelay = 10 * time.Second
					continue
				}

				//CA chain accessible.  Re-do the verified transports

				logger.Log.Infof("CA trust chain has changed, re-doing Redfish HTTP transports.")
				rfClient, err = hms_certs.CreateRetryableHTTPClientPair(caURI, dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
				if err != nil {
					logger.Log.Errorf("Error creating TLS-verified transport: %v, retrying...",
						err)
					continue
				}
				logger.Log.Infof("Locking RF operations...")
				rfClientLock.Lock() //waits for all RW locks to release
				tchain := hms_certs.NewlineToTuple(caChain)
				secInfo := trsapi.TRSHTTPLocalSecurity{CACertBundleData: tchain}
				err = TLOC_rf.SetSecurity(secInfo)
				if err != nil {
					logger.Log.Errorf("Error setting TLOC security info: %v, retrying...",
						err)
					rfClientLock.Unlock()
					continue
				} else {
					logger.Log.Info("TRS CA security updated.")
				}
				prevCaChain = caChain

				//update RF tloc and rfclient to the global areas! //TODO im not sure what part of this code is still needed; im guessing part of it at least!
				domainGlobals.RFTloc = &TLOC_rf
				domainGlobals.RFHttpClient = rfClient
				//hsmGlob.RFTloc = &TLOC_rf
				//hsmGlob.RFHttpClient = rfClient
				//HSM.Init(&hsmGlob)
				rfClientLock.Unlock()
				RFTransportReady = true
				domainGlobals.RFTransportReady = &RFTransportReady
			}
		}
	}()

	///////////////////////////////
	//INITIALIZATION
	//////////////////////////////
	domain.Init(&domainGlobals)

	dlockTimeout := 60
	pwrSampleInterval := 30
	statusTimeout := 30
	statusHttpRetries := 3
	maxIdleConns := 4000
	maxIdleConnsPerHost := 4 // 4000 / 4 = 4 open conns for each of 1000 BMCs

	envstr = os.Getenv("PCS_POWER_SAMPLE_INTERVAL")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_POWER_SAMPLE_INTERVAL, defaulting to %d",
				pwrSampleInterval)
		} else {
			logger.Log.Infof("Using PCS_POWER_SAMPLE_INTERVAL: %v", tps)
			pwrSampleInterval = tps
		}
	}
	envstr = os.Getenv("PCS_DISTLOCK_TIMEOUT")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_DISTLOCK_TIMEOUT, defaulting to %d",
				dlockTimeout)
		} else {
			logger.Log.Infof("Using PCS_DISTLOCK_TIMEOUT: %v", tps)
			dlockTimeout = tps
		}
	}
	envstr = os.Getenv("PCS_STATUS_TIMEOUT")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_STATUS_TIMEOUT, defaulting to %d",
				statusTimeout)
		} else {
			logger.Log.Infof("Using PCS_STATUS_TIMEOUT: %v", tps)
			statusTimeout = tps
		}
	}
	envstr = os.Getenv("PCS_STATUS_HTTP_RETRIES")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_STATUS_HTTP_RETRIES, defaulting to %d",
				statusHttpRetries)
		} else {
			logger.Log.Infof("Using PCS_STATUS_HTTP_RETRIES: %v", tps)
			statusHttpRetries = tps
		}
	}
	envstr = os.Getenv("PCS_MAX_IDLE_CONNS")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_MAX_IDLE_CONNS, defaulting to %d",
				maxIdleConns)
		} else {
			logger.Log.Infof("Using PCS_MAX_IDLE_CONNS: %v", tps)
			maxIdleConns = tps
		}
	}
	envstr = os.Getenv("PCS_MAX_IDLE_CONNS_PER_HOST")
	if envstr != "" {
		tps, err := strconv.Atoi(envstr)
		if err != nil {
			logger.Log.Errorf("Invalid value of PCS_MAX_IDLE_CONNS_PER_HOST, defaulting to %d",
				maxIdleConnsPerHost)
		} else {
			logger.Log.Infof("Using PCS_MAX_IDLE_CONNS_PER_HOST: %v", tps)
			maxIdleConnsPerHost = tps
		}
	}
	envstr = os.Getenv("PCS_JWKS_URL")
	if envstr != "" {
		jwksURL = envstr
	}

	// Initialize token authorization and load JWKS well-knowns from .well-known endpoint
	if jwksURL != "" {
		logger.Log.Info("Fetching public key from server...")
		for i := 0; i <= 5; i++ {
			err = api.FetchPublicKeyFromURL(jwksURL)
			if err != nil {
				logger.Log.Errorf("Failed to initialize auth token: %v", err)
				time.Sleep(time.Duration(jwksFetchInterval) * time.Second)
				continue
			}
			logger.Log.Info("Initialized the auth token successfully.")
			break
		}
	}

	domain.PowerStatusMonitorInit(&domainGlobals,
		(time.Duration(dlockTimeout) * time.Second),
		logger.Log, (time.Duration(pwrSampleInterval) * time.Second),
		statusTimeout, statusHttpRetries, maxIdleConns, maxIdleConnsPerHost)

	domain.StartRecordsReaper()

	///////////////////////////////
	//SIGNAL HANDLING -- //TODO does this need to move up ^ so it happens sooner?
	//////////////////////////////

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	idleConnsClosed := make(chan struct{})
	go func() {
		<-c
		Running = false

		//TODO; cannot Cancel the context on retryablehttp; because I havent set them up!
		//cancel()

		// Gracefully shutdown the HTTP server.
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			logger.Log.Infof("HTTP server Shutdown: %v", err)
		}

		ctx := context.Background()
		if restSrv != nil {
			if err := restSrv.Shutdown(ctx); err != nil {
				logger.Log.Panic("ERROR: Unable to stop REST collection server!")
			}
		}

		close(idleConnsClosed)
	}()

	///////////////////////
	// START
	///////////////////////

	//Master Control

	if pcs.runControl {
		logger.Log.Info("Starting control loop")
		//Go start control loop!
	} else {
		logger.Log.Info("NOT starting control loop")
	}
	//Rest Server
	waitGroup.Add(1)
	doRest(defaultPORT)

	//////////////////////
	// WAIT FOR GOD
	/////////////////////

	waitGroup.Wait()
	logger.Log.Info("HTTP server shutdown, waiting for idle connection to close...")
	<-idleConnsClosed
	logger.Log.Info("Done. Exiting.")

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

// schemaConfig holds the configuration for the Postgres schema initialization command
type schemaConfig struct {
	step         uint
	forceStep    int
	fresh        bool
	migrationDir string
}

// add environment variable usage to the command flags
func addEnvVarToUsage(flags *pflag.FlagSet) {

	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Name == "h" {
			// Skip help flag
			return
		}

		envVarName := flagToEnvVarName(flag.Name)
		flag.Usage = fmt.Sprintf("(%s) %s", envVarName, flag.Usage)
	})
}

// createEnvVarHelp creates a help function that adds environment variable usage to the command flags
func createEnvVarHelp(defaultHelpFunc func(cmd *cobra.Command, args []string)) func(cmd *cobra.Command, args []string) {

	return func(cmd *cobra.Command, args []string) {
		addEnvVarToUsage(cmd.Flags())

		defaultHelpFunc(cmd, args)
	}
}

// createPostgresInitCommand creates a cobra command to initialize and migrate the Postgres database for Power Control Service
func createPostgresInitCommand(postgres *storage.PostgresConfig, schema *schemaConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init-postgres",
		Short: "Initialize and migrate the Postgres database for Power Control Service",
		Long:  "Initialize and migrate the Postgres database for Power Control Service",
		Run: func(cmd *cobra.Command, args []string) {
			// Initialize and migrate the Postgres database
			migrateSchema(schema, postgres, nil)
		},
	}

	cmd.Flags().UintVar(&schema.step, "schema-step", schema.step, "Migration step to apply")
	cmd.Flags().IntVar(&schema.forceStep, "schema-force-step", schema.forceStep, "Force migration to a specific step")
	cmd.Flags().BoolVar(&schema.fresh, "schema-fresh", schema.fresh, "Drop all tables and start fresh")
	cmd.Flags().StringVar(&schema.migrationDir, "schema-migrations", schema.migrationDir, "Directory for migration files")

	return cmd
}

// createRootCommand creates the root command power-control command
func createRootCommand(pcs *pcsConfig, etcd *etcdConfig, postgres *storage.PostgresConfig) *cobra.Command {
	// root command to run PCS and parent the Postgres initialization command
	rootCommand := &cobra.Command{
		Use:   "power-control",
		Short: "Power Control Service",
		Long:  "Power Control Service",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Parse environment variables for flags
			err := parseFlagEnvVars(cmd.Flags())
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				cmd.Usage()
				os.Exit(1)
			}
		},

		Run: func(cmd *cobra.Command, args []string) {
			runPCS(pcs, etcd, postgres)
		},
	}

	rootCommand.Flags().StringVar(&pcs.stateManagerServer, "sms-server", defaultSMSServer, "SMS Server")
	rootCommand.Flags().BoolVar(&pcs.runControl, "run-control", pcs.runControl, "run control loop; false runs API only") //this was a flag useful for dev work
	rootCommand.Flags().BoolVar(&pcs.hsmLockEnabled, "hsmlock-enabled", true, "Use HSM Locking")                         // This was a flag useful for dev work
	rootCommand.Flags().BoolVar(&pcs.vaultEnabled, "vault-enabled", true, "Should vault be used for credentials?")
	rootCommand.Flags().StringVar(&pcs.vaultKeypath, "vault-keypath", "secret/hms-creds",
		"Keypath for Vault credentials.")
	rootCommand.Flags().IntVar(&pcs.credCacheDuration, "cred-cache-duration", 600,
		"Duration in seconds to cache vault credentials.")

	rootCommand.Flags().IntVar(&pcs.maxNumCompleted, "max-num-completed", defaultMaxNumCompleted, "Maximum number of completed records to keep.")
	rootCommand.Flags().IntVar(&pcs.expireTimeMins, "expire-time-mins", defaultExpireTimeMins, "The time, in mins, to keep completed records.")

	// ETCD flags
	rootCommand.Flags().BoolVar(&etcd.disableSizeChecks, "etcd-disable-size-checks", false, "Disables checking object size before storing and doing message truncation and paging.")
	rootCommand.Flags().IntVar(&etcd.pageSize, "etcd-page-size", storage.DefaultEtcdPageSize, "The maximum number of records to put in each etcd entry.")
	rootCommand.Flags().IntVar(&etcd.maxMessageLength, "etcd-max-transition-message-length", storage.DefaultMaxMessageLen, "The maximum length of messages per task in a transition.")
	rootCommand.Flags().IntVar(&etcd.maxObjectSize, "etcd-max-object-size", storage.DefaultMaxEtcdObjectSize, "The maximum data size in bytes for objects in etcd.")

	// JWKS URL flag
	rootCommand.Flags().StringVar(&jwksURL, "jwks-url", "", "Set the JWKS URL to fetch public key for validation")

	// Postgres flags
	rootCommand.PersistentFlags().StringVarP(&postgres.Host, "postgres-host", "", postgres.Host, "Postgres host as IP address or name")
	rootCommand.PersistentFlags().StringVarP(&postgres.User, "postgres-user", "", postgres.User, "Postgres username")
	rootCommand.PersistentFlags().StringVarP(&postgres.Password, "postgres-password", "", postgres.Password, "Postgres password")
	rootCommand.PersistentFlags().StringVarP(&postgres.DBName, "postgres-dbname", "", postgres.DBName, "Postgres database name")
	rootCommand.PersistentFlags().StringVarP(&postgres.Opts, "postgres-opts", "", postgres.Opts, "Postgres database options")
	rootCommand.PersistentFlags().UintVarP(&postgres.Port, "postgres-port", "", postgres.Port, "Postgres port")
	rootCommand.PersistentFlags().Uint64VarP(&postgres.RetryCount, "postgres-retry_count", "", postgres.RetryCount, "Number of times to retry connecting to Postgres database before giving up")
	rootCommand.PersistentFlags().Uint64VarP(&postgres.RetryWait, "postgres-retry_wait", "", postgres.RetryWait, "Seconds to wait between retrying connection to Postgres")
	rootCommand.PersistentFlags().BoolVarP(&postgres.Insecure, "postgres-insecure", "", postgres.Insecure, "Don't enforce certificate authority for Postgres")

	// Add environment variables to usage by overriding the usage function
	usageFunc := rootCommand.UsageFunc()
	rootCommand.SetUsageFunc(func(cmd *cobra.Command) error {
		addEnvVarToUsage(cmd.Flags())
		return usageFunc(cmd)
	})

	return rootCommand
}

// flagToEnvVarName converts a flag name to an environment variable name
func flagToEnvVarName(flag string) string {
	// TODO: It might be nice to have a standard prefix, but for now
	// this would have a bit a ripple effect on existing code.
	// envVarName := "PCS_" + flag
	envVarName := strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))

	return envVarName
}

// envVarError creates an error message for invalid environment variable values
func envVarError(name string, value string, varType string) error {
	return fmt.Errorf("Error: invalid value \"%s\" for environment variable \"%s\". Expected a value of type %s", value, name, varType)
}

// parseFlagEnvVar parses the environment variable for a given flag and sets the flag value accordingly
func parseFlagEnvVar(flag *pflag.Flag) error {
	envVarName := flagToEnvVarName(flag.Name)
	envVarValue := os.Getenv(envVarName)

	var err error
	if envVarValue != "" {
		switch flag.Value.Type() {
		case "string":
			flag.Value.Set(envVarValue)
		case "bool":
			_, err = strconv.ParseBool(envVarValue)
		case "int":
			_, err = strconv.Atoi(envVarValue)
		case "uint":
			_, err = strconv.ParseUint(envVarValue, 10, 64)
		default:
			err = fmt.Errorf("unsupported flag type: %s", flag.Value.Type())
		}

		if err != nil {
			return envVarError(envVarName, envVarValue, flag.Value.Type())
		}

		// The value is valid, set the flag
		flag.Value.Set(envVarValue)
	}

	return nil
}

// parseFlagEnvVars iterates over all flags, parses their corresponding environment variables
// and sets the flag values accordingly. If any error occurs, it returns the error.
func parseFlagEnvVars(flags *pflag.FlagSet) error {
	var err error
	flags.VisitAll(func(flag *pflag.Flag) {
		// Skip help flag
		if flag.Name == "help" || flag.Name == "h" {
			return
		}

		// We already have a error, so skip the rest of the flags
		if err != nil {
			return
		}

		if !flag.Changed {
			err = parseFlagEnvVar(flag)
		}
	})

	return err
}

func main() {
	pcs := pcsConfig{
		hsmLockEnabled:    true,
		runControl:        false,
		credCacheDuration: 600,
	}
	postgres := storage.DefaultPostgresConfig()
	var etcd etcdConfig
	var schema schemaConfig

	logger.Init()

	rootCommand := createRootCommand(&pcs, &etcd, &postgres)
	// Add the Postgres initialization command
	rootCommand.AddCommand(createPostgresInitCommand(&postgres, &schema))

	if err := rootCommand.Execute(); err != nil {
		os.Exit(1)
	}
}

func doRest(serverPort string) {

	logger.Log.Info("**RUNNING -- Listening on " + defaultPORT)

	srv := &http.Server{Addr: ":" + serverPort}
	router := api.NewRouter()

	http.Handle("/", router)

	go func() {
		defer waitGroup.Done()
		if err := srv.ListenAndServe(); err != nil {
			// Cannot panic because this is probably just a graceful shutdown.
			logger.Log.Error(err)
			logger.Log.Info("REST collection server shutdown.")
		}
	}()

	logger.Log.Info("REST collection server started on port " + serverPort)
	restSrv = srv
}
