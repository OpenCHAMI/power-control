package main

import (
	"context"
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
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/OpenCHAMI/power-control/v2/internal/api"
	"github.com/OpenCHAMI/power-control/v2/internal/credstore"
	"github.com/OpenCHAMI/power-control/v2/internal/domain"
	"github.com/OpenCHAMI/power-control/v2/internal/hsm"
	"github.com/OpenCHAMI/power-control/v2/internal/logger"
	"github.com/OpenCHAMI/power-control/v2/internal/storage"
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

const (
	fakeVaultUserEnv     = "PCS_FAKE_VAULT_REDFISH_USER"
	fakeVaultPasswordEnv = "PCS_FAKE_VAULT_REDFISH_PASSWORD"
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
	fakeVaultEnabled   bool
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

// fakeVault holds fake BMC credentials to use when the fake Vault is enabled.
type fakeVault struct {
	User     string
	Password string
}

// oauth2Config holds the OAuth2 configuration for SMD communication.
type oauth2Config struct {
	tokenURL     string
	clientID     string
	clientSecret string
	scopes       []string
}

// runPCS runs the Power Control Service (PCS).
func runPCS(pcs *pcsConfig, etcd *etcdConfig, postgres *storage.PostgresConfig, oauth2Config *oauth2Config) {
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
	logger.Log.Info("Fake Vault Enabled: ", pcs.fakeVaultEnabled)
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
	rfClient, err := hms_certs.CreateRetryableHTTPClientPair("", dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
	if err != nil {
		logger.Log.Fatalf("Error creating RF HTTP client: %v", err)
	}

	svcClient, err = hms_certs.CreateRetryableHTTPClientPair("", dfltMaxHTTPTimeout, dfltMaxHTTPRetries, dfltMaxHTTPBackoff)
	if err != nil {
		logger.Log.Fatalf("Error creating SMD HTTP client: %v", err)
	}

	// Chain HTTP clients with OAuth2 if configured
	if oauth2Config != nil {
		clientConfig := &clientcredentials.Config{
			ClientID:     oauth2Config.clientID,
			ClientSecret: oauth2Config.clientSecret,
			TokenURL:     oauth2Config.tokenURL,
			Scopes:       oauth2Config.scopes,
			AuthStyle:    oauth2.AuthStyleInHeader,
		}

		logger.Log.Info("Configuring SMD client with OAuth2")

		ctx := context.Background()
		ts := clientConfig.TokenSource(ctx)

		if c := svcClient.SecureClient; c != nil {
			baseTransport := c.HTTPClient.Transport
			if baseTransport == nil {
				baseTransport = http.DefaultTransport
			}
			c.HTTPClient.Transport = &oauth2.Transport{
				Source: ts,
				Base:   baseTransport,
			}
		}
		if c := svcClient.InsecureClient; c != nil {
			baseTransport := c.HTTPClient.Transport
			if baseTransport == nil {
				baseTransport = http.DefaultTransport
			}
			c.HTTPClient.Transport = &oauth2.Transport{
				Source: ts,
				Base:   baseTransport,
			}
		}
	}

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
		tmpDistLockImplementation := &storage.PostgresLockProvider{
			Config: *postgres,
		}
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

	// TODO The original Vault configuration is awkward for proceeding with https://github.com/OpenCHAMI/roadmap/issues/74
	// Ideally we'd use a switch on a string flag, but the current flag is a bool. It's furthermore default true, so
	// annoying to make mutually exclusive.
	//
	// Handling that well would require breaking changes. Pending expanded support for several SecureStorage
	// implementations, the kludgy approach is to expect that setting the new default false --fake-vault-enabled
	// means you do want that, so we can use the ugly else-if chain below, with the original disabled empty
	// credstore.VAULTv0 as the pseudo-default case.
	if pcs.fakeVaultEnabled {
		var mockUser, mockPass string
		// fakeVaultUserEnv and fakeVaultPasswordEnv are constant env-only args. We may or may not want full args
		// for them following a breaking redesign of credential store configuration. Env only is easier to work
		// with, since it doesn't require config struct plumbing, and is simpler to refactor as such. Env only is kinda
		// okay for them, since we don't need them in any other CLI command.
		if user, exists := os.LookupEnv(fakeVaultUserEnv); exists {
			mockUser = user
		} else {
			logger.Log.Errorf("%s must be set if fake Vault is enabled", fakeVaultUserEnv)
			os.Exit(1)
		}
		if pass, exists := os.LookupEnv(fakeVaultPasswordEnv); exists {
			mockPass = pass
		} else {
			logger.Log.Errorf("%s must be set if fake Vault is enabled", fakeVaultPasswordEnv)
			os.Exit(1)
		}
		tmpCS := &credstore.MockStore{Username: mockUser, Password: mockPass}
		CS = tmpCS
	} else if pcs.vaultEnabled {
		//Vault CONFIGURATION
		// The CRAY_VAULT_AUTH_PATH and similar config for the original Vault SecureStorage are buried down in its package,
		// and can't be set via flags:
		// https://github.com/Cray-HPE/hms-securestorage/blob/v1.18.0/vaultAdapter.go#L382-L409
		CS = &credstore.VAULTv0{}
		var credStoreGlob credstore.CREDSTORE_GLOBALS
		credStoreGlob.NewGlobals(logger.Log, &Running, pcs.credCacheDuration, pcs.vaultKeypath)
		CS.Init(&credStoreGlob)
	} else {
		tmpCS := &credstore.VAULTv0{}
		CS = tmpCS
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
