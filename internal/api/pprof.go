package api

import (
	"net/http/pprof"
	_ "net/http/pprof"

	"github.com/gorilla/mux"
)

func RegisterPProfHandlers(router *mux.Router) {
	// Main profiling entry point
	router.HandleFunc("/v1/debug/pprof/", pprof.Index) // Index listing all pprof endpoints

	// Specific profiling handlers
	router.HandleFunc("/v1/debug/pprof/cmdline", pprof.Cmdline) // Command-line arguments
	router.HandleFunc("/v1/debug/pprof/profile", pprof.Profile) // CPU profile (default: 30 seconds)
	router.HandleFunc("/v1/debug/pprof/symbol", pprof.Symbol)   // Symbol resolution for addresses
	router.HandleFunc("/v1/debug/pprof/trace", pprof.Trace)     // Execution trace (default: 1 second)

	// Additional profiling endpoints
	router.Handle("/v1/debug/pprof/allocs", pprof.Handler("allocs"))             // Heap allocation samples
	router.Handle("/v1/debug/pprof/block", pprof.Handler("block"))               // Goroutine blocking events
	router.Handle("/v1/debug/pprof/goroutine", pprof.Handler("goroutine"))       // Stack traces of all goroutines
	router.Handle("/v1/debug/pprof/heap", pprof.Handler("heap"))                 // Memory heap profile
	router.Handle("/v1/debug/pprof/mutex", pprof.Handler("mutex"))               // Mutex contention profile
	router.Handle("/v1/debug/pprof/threadcreate", pprof.Handler("threadcreate")) // Stack traces of thread creation
}
