package main

import (
	"github.com/andrewsl105/fleetpocket/server/internal/app"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
)

func runServe() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return app.Serve(ctx, cancel, cfg, app.Options{})
}
