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
	"github.com/caarlos0/env/v11"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
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

type pcsConfig struct {
	vaultEnabled       bool
	vaultKeypath       string
	stateManagerServer string
	hsmLockEnabled     bool
	runControl         bool
	credCacheDuration  int
	maxNumCompleted    int
	expireTimeMins     int
	maxMessageLength   int
}

type etcdConfig struct {
	etcdDisableSizeChecks bool
	etcdPageSize          int
	etcdMaxObjectSize     int
}

func run(pcs pcsConfig, etcd etcdConfig, postgres storage.PostgresConfig) {

	var err error
	logger.Init()
	logger.Log.Error()

	serviceName, err = base.GetServiceInstanceName()
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
			DisableSizeChecks: etcd.etcdDisableSizeChecks,
			PageSize:          etcd.etcdPageSize,
			MaxMessageLen:     pcs.maxMessageLength,
			MaxEtcdObjectSize: etcd.etcdMaxObjectSize,
		}
		DSP = tmpStorageImplementation
		logger.Log.Info("Storage Provider: In Memory")
		tmpDistLockImplementation := &storage.MEMLockProvider{}
		DLOCK = tmpDistLockImplementation
		logger.Log.Info("Distributed Lock Provider: In Memory")
	} else if envstr == "ETCD" {
		tmpStorageImplementation := &storage.ETCDStorage{
			Logger:            logger.Log,
			DisableSizeChecks: etcd.etcdDisableSizeChecks,
			PageSize:          etcd.etcdPageSize,
			MaxMessageLen:     pcs.maxMessageLength,
			MaxEtcdObjectSize: etcd.etcdMaxObjectSize,
		}
		DSP = tmpStorageImplementation
		logger.Log.Info("Storage Provider: ETCD")
		tmpDistLockImplementation := &storage.ETCDLockProvider{}
		DLOCK = tmpDistLockImplementation
		logger.Log.Info("Distributed Lock Provider: ETCD")
	} else if envstr == "POSTGRES" {
		tmpStorageImplementation := &storage.PostgresStorage{
			Config: postgres,
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

	err = DLOCK.Init(logger.Log)
	if err != nil {
		logger.Log.Errorf("Error initializing distributed lock provider: %v", err)
		os.Exit(1)
	}

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

func parsePostgresEnvVars(config *storage.PostgresConfig) error {
	err := env.Parse(config)
	if err != nil {
		return fmt.Errorf("Error parsing environment variables: %v", err)
	}

	return nil
}

func main() {
	pcs := pcsConfig{
		hsmLockEnabled:    true,
		runControl:        false,
		credCacheDuration: 600,
	}
	postgres := storage.DefaultPostgresConfig()
	var etcd etcdConfig

	// Parse environment variables
	err := parsePostgresEnvVars(&postgres)
	if err != nil {
		logger.Log.Errorf("Error parsing Postgres environment variables: %v", err)
		os.Exit(1)
	}

	///////////////////////////////
	//ENVIRONMENT PARSING
	//////////////////////////////
	cmd := &cobra.Command{
		Use:   "power-control",
		Short: "Power Control Service",
		Long:  "Power Control Service",
		Run: func(cmd *cobra.Command, args []string) {
			run(pcs, etcd, postgres)
		},
	}

	cmd.Flags().StringVar(&pcs.stateManagerServer, "sms_server", defaultSMSServer, "SMS Server")
	cmd.Flags().BoolVar(&pcs.runControl, "run_control", pcs.runControl, "run control loop; false runs API only") //this was a flag useful for dev work
	cmd.Flags().BoolVar(&pcs.hsmLockEnabled, "hsmlock_enabled", true, "Use HSM Locking")                         // This was a flag useful for dev work
	cmd.Flags().BoolVar(&pcs.vaultEnabled, "vault_enabled", true, "Should vault be used for credentials?")
	cmd.Flags().StringVar(&pcs.vaultKeypath, "vault_keypath", "secret/hms-creds",
		"Keypath for Vault credentials.")
	cmd.Flags().IntVar(&pcs.credCacheDuration, "cred_cache_duration", 600,
		"Duration in seconds to cache vault credentials.")

	cmd.Flags().IntVar(&pcs.maxNumCompleted, "max_num_completed", defaultMaxNumCompleted, "Maximum number of completed records to keep.")
	cmd.Flags().IntVar(&pcs.expireTimeMins, "expire_time_mins", defaultExpireTimeMins, "The time, in mins, to keep completed records.")
	cmd.Flags().BoolVar(&etcd.etcdDisableSizeChecks, "etcd_disable_size_checks", false, "Disables checking object size before storing and doing message truncation and paging.")
	cmd.Flags().IntVar(&etcd.etcdPageSize, "etcd_page_size", storage.DefaultEtcdPageSize, "The maximum number of records to put in each etcd entry.")
	cmd.Flags().IntVar(&pcs.maxMessageLength, "max_transition_message_length", storage.DefaultMaxMessageLen, "The maximum length of messages per task in a transition.")
	cmd.Flags().IntVar(&etcd.etcdMaxObjectSize, "etcd_max_object_size", storage.DefaultMaxEtcdObjectSize, "The maximum data size in bytes for objects in etcd.")
	cmd.Flags().StringVar(&jwksURL, "jwks-url", "", "Set the JWKS URL to fetch public key for validation")

	// Postgres flags
	cmd.Flags().StringVarP(&postgres.Host, "postgres_host", "", postgres.Host, "(PCS_POSTGRES_HOST) Postgres host as IP address or name")
	cmd.Flags().StringVarP(&postgres.User, "postgres_user", "", postgres.User, "(PCS_POSTGRES_USER) Postgres username")
	cmd.Flags().StringVarP(&postgres.Password, "postgres_password", "", postgres.Password, "(PCS_POSTGRES_PASSWORD) Postgres password")
	cmd.Flags().StringVarP(&postgres.DBName, "postgres_dbname", "", postgres.DBName, "(PCS_POSTGRES_DBNAME) Postgres database name")
	cmd.Flags().StringVarP(&postgres.Opts, "postgres_opts", "", postgres.Opts, "(PCS_POSTGRES_OPTS) Postgres database options")
	cmd.Flags().UintVarP(&postgres.Port, "postgres_port", "", postgres.Port, "(PCS_POSTGRES_PORT) Postgres port")
	cmd.Flags().Uint64VarP(&postgres.RetryCount, "postgres_retry_count", "", postgres.RetryCount, "(PCS_POSTGRES_RETRY_COUNT) Number of times to retry connecting to Postgres database before giving up")
	cmd.Flags().Uint64VarP(&postgres.RetryWait, "postgres_retry_wait", "", postgres.RetryWait, "(PCS_POSTGRES_RETRY_WAIT) Seconds to wait between retrying connection to Postgres")
	cmd.Flags().BoolVarP(&postgres.Insecure, "postgres_insecure", "", postgres.Insecure, "(PCS_POSTGRES_INSECURE) Don't enforce certificate authority for Postgres")

	if err := cmd.Execute(); err != nil {
		logger.Log.Errorf("Error executing command: %v", err)
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
