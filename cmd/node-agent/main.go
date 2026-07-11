// node-agent is the Design-B data plane: one per GPU node, owns local NVMe
// staging and both checkpoint paths. M1 scope: HTTP API + manual trigger.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/wokritmundung/devdesk/pkg/agent"
	"github.com/wokritmundung/devdesk/pkg/api"
	"github.com/wokritmundung/devdesk/pkg/checkpoint"
	"github.com/wokritmundung/devdesk/pkg/staging"
)

func main() {
	listen := flag.String("listen", ":8484", "listen address")
	dataDir := flag.String("data-dir", "/var/lib/rewarm/snapshots", "local NVMe staging root")
	engineURL := flag.String("engine-url", "", "default vLLM engine base URL, e.g. http://127.0.0.1:8000")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	store, err := staging.NewStore(*dataDir)
	if err != nil {
		log.Error("staging store", "err", err)
		os.Exit(1)
	}
	if n, err := store.Sweep(); err == nil && n > 0 {
		log.Info("swept incomplete snapshots", "count", n)
	}

	srv := &agent.Server{
		Store: store,
		Paths: map[api.Mode]checkpoint.Checkpointer{
			api.ModeEngineNative: &checkpoint.EngineNative{DefaultEngineURL: *engineURL},
			api.ModeProcess:      &checkpoint.Process{},
		},
		Log: log,
	}
	log.Info("rewarm node-agent listening", "addr", *listen, "dataDir", *dataDir)
	if err := http.ListenAndServe(*listen, srv.Handler()); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
