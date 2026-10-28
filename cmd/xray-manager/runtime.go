package main

import (
	"os"

	"github.com/degritsenko/xray-ui/internal/app"
)

type runtimeSettings struct {
	dir       string
	store     *app.Store
	state     app.State
	auth      *app.Auth
	generated string
}

// Each process starts from environment variables in a private temporary directory.
func newRuntime(env func(string) string) (*runtimeSettings, error) {
	dir, err := os.MkdirTemp("", "xray-ui-*")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()
	store := app.NewStore(dir)
	state, err := store.LoadOrImport(env)
	if err != nil {
		return nil, err
	}
	auth, generated, err := app.OpenAuth(dir, env("PANEL_PASSWORD"))
	if err != nil {
		return nil, err
	}
	success = true
	return &runtimeSettings{dir: dir, store: store, state: state, auth: auth, generated: generated}, nil
}
