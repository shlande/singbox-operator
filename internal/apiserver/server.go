package apiserver

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Server is the HTTP API server embedded in the controller-runtime manager.
type Server struct {
	BindAddress string
	TemplateRef string
	Client      client.Client
}

// Start implements manager.Runnable. Starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/client-config/", s.handleClientConfig)
	srv := &http.Server{Addr: s.BindAddress, Handler: mux}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	logger.Info("Starting client config API server", "address", s.BindAddress)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// NeedLeaderElection returns false — API server runs on all replicas.
func (s *Server) NeedLeaderElection() bool { return false }
