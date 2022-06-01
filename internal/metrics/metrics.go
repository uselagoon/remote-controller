package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewServer returns a *http.Server serving prometheus metrics in a new
// goroutine.
// Caller should defer Shutdown() for cleanup.
func NewServer(log logr.Logger, addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	s := http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  16 * time.Second,
		WriteTimeout: 16 * time.Second,
	}
	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Error(fmt.Errorf("metrics server did not shut down cleanly"), err.Error())
		}
	}()
	return &s
}
