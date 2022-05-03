package metrics

import (
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
			log.Error(fmt.Errorf("metrics server did not shut down cleanly"), err)
		}
	}()
	return &s
}
