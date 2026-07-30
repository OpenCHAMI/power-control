package taskrun

import (
	"encoding/pem"
	"flag"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

var logLevel logrus.Level

func TestMain(m *testing.M) {
	var logLevelInt int
	flag.IntVar(&logLevelInt, "logLevel", int(logrus.ErrorLevel),
		"set log level (0=Panic, 1=Fatal, 2=Error 3=Warn, 4=Info, 5=Debug, 6=Trace)")
	flag.Parse()
	logLevel = logrus.Level(logLevelInt)
	log.Printf("logLevel set to %v", logLevel)
	os.Exit(m.Run())
}

func createLogger() *logrus.Logger {
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	l.SetLevel(logrus.Level(logLevel))
	l.SetReportCaller(true)
	return l
}

var svcName = "TestMe"

func TestInit(t *testing.T) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())
	if tloc.taskMap == nil {
		t.Errorf("failed to create task map")
	}
	if tloc.clientMap == nil {
		t.Errorf("failed to create client map")
	}
	if tloc.svcName != svcName {
		t.Errorf("failed to set service name")
	}
}

// launchHandler handles requests for the test HTTP servers. Depending on
// request headers it can return 503 for retry testing, stall until signalled,
// or return 200 immediately.

var handlerLogger *testing.T
var handlerSleep int = 2
var retrySleep int = 0
var nRetries int32 = 0

func launchHandler(w http.ResponseWriter, req *http.Request) {
	singletonRetry := false
	alwaysFail := req.Header.Get("Trs-Fail-All-Retries") != ""

	if !alwaysFail {
		singletonRetry = atomic.AddInt32(&nRetries, -1) >= 0
	}

	if singletonRetry || alwaysFail {
		if singletonRetry {
			atomic.AddInt32(&nRetries, -1)
		}
		time.Sleep(time.Duration(retrySleep) * time.Second)
		retrySleep = 0
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"Message":"Service Unavailable"}`))
		return
	}

	if req.Header.Get("Trs-Context-Timeout") != "" {
		stallHandler(w, req)
		return
	}

	time.Sleep(time.Duration(handlerSleep) * time.Second)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))
}

var stallCancel chan bool

func stallHandler(w http.ResponseWriter, req *http.Request) {
	time.Sleep(100 * time.Millisecond)
	<-stallCancel
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))
}

func TestLaunch(t *testing.T) {
	testLaunch(t, 5, false, false, 8)
}

func TestSecureLaunch(t *testing.T) {
	testLaunch(t, 1, true, false, 8)
}

func TestSecureLaunchBadCert(t *testing.T) {
	// Despite the cert being bad, TRS falls back to the insecure client and succeeds.
	testLaunch(t, 1, true, true, 8)
}

func TestLaunchZeroTimeout(t *testing.T) {
	// Zero should cause the default timeout to be applied; if not, the test hangs.
	testLaunch(t, 5, false, false, 0)
}

func testLaunch(t *testing.T, numTasks int, testSecure bool, useBadCert bool, timeout time.Duration) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	var srv *httptest.Server
	if testSecure {
		srv = httptest.NewTLSServer(http.HandlerFunc(launchHandler))
		certData := "BAD CERT"
		if !useBadCert {
			certData = string(pem.EncodeToMemory(
				&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw},
			))
		}
		if err := tloc.SetSecurity(TRSHTTPLocalSecurity{CACertBundleData: certData}); err != nil {
			t.Errorf("SetSecurity() failed: %v", err)
			return
		}
	} else {
		srv = httptest.NewServer(http.HandlerFunc(launchHandler))
	}
	defer srv.Close()

	handlerLogger = t

	req, _ := http.NewRequest("GET", srv.URL, nil)
	tproto := HttpTask{Request: req, Timeout: timeout * time.Second}
	tList := tloc.CreateTaskList(&tproto, numTasks)

	tch, err := tloc.Launch(&tList)
	if err != nil {
		t.Errorf("Launch() failed: %v", err)
	}

	nDone, nErr := 0, 0
	for {
		tdone := <-tch
		nDone++
		if tdone == nil {
			t.Errorf("Launch chan returned nil ptr")
		} else if tdone.Request == nil {
			t.Errorf("Launch chan returned nil Request")
		} else if tdone.Request.Response == nil {
			t.Errorf("Launch chan returned nil Response")
		} else {
			if tdone.Request.Response.StatusCode != http.StatusOK {
				t.Errorf("Launch chan returned bad status: %v", tdone.Request.Response.StatusCode)
				nErr++
			}
			if tdone.Err != nil && *tdone.Err != nil {
				t.Errorf("Launch chan returned error: %v", *tdone.Err)
			}
		}
		if nDone == len(tList) {
			break
		}
	}

	if nErr != 0 {
		t.Errorf("Got %d errors from Launch", nErr)
	}
}

func TestLaunchTimeout(t *testing.T) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())
	srv := httptest.NewServer(http.HandlerFunc(stallHandler))
	defer srv.Close()

	handlerLogger = t

	req, _ := http.NewRequest("GET", srv.URL, nil)
	tproto := HttpTask{
		Request: req,
		Timeout: 3 * time.Second,
		CPolicy: ClientPolicy{
			Retry: RetryPolicy{Retries: 1, BackoffTimeout: 3 * time.Second},
		},
	}
	tList := tloc.CreateTaskList(&tproto, 1)
	stallCancel = make(chan bool, 1)

	tch, err := tloc.Launch(&tList)
	if err != nil {
		t.Errorf("Launch() failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	for range tList {
		<-tch
		stallCancel <- true
	}
	close(stallCancel)
}
