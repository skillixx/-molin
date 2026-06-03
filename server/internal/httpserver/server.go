package httpserver

import (
	"net/http"
	"time"

	"molin/server/internal/config"
)

func New(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         cfg.APIHost + ":" + cfg.APIPort,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}
