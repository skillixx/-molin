package bootstrap

import (
	"fmt"
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/httpserver"
	"molin/server/internal/router"
)

type App struct {
	Config config.Config
	Server *http.Server
}

func NewApp() (*App, error) {
	cfg := config.Load()
	routes := router.NewRouter()

	srv := httpserver.New(cfg, routes)

	return &App{
		Config: cfg,
		Server: srv,
	}, nil
}

func (a *App) Run() error {
	fmt.Printf("API server listening on %s\n", a.Server.Addr)
	return a.Server.ListenAndServe()
}
