package server

import (
	"net/http"
	"time"

	"znt/internal/app/core"
)

type healthResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Mode    string `json:"mode,omitempty"`
	Time    string `json:"time"`
}

func registerHealthRoutes(mux *http.ServeMux, appCore *core.Core) {
	cfg := appCore.Config
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, healthResponse{
			Service: cfg.ServiceName,
			Version: cfg.Version,
			Status:  "ok",
			Time:    time.Now().UTC().Format(time.RFC3339Nano),
		}, http.StatusOK)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": cfg.Version}, http.StatusOK)
	})
}
