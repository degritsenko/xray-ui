package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/degritsenko/xray-ui/internal/app"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	dir := env("STATE_DIR", "/opt/xray/state")
	store := app.NewStore(dir)
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "list":
		state, err := store.Load()
		if err != nil {
			return errors.New("no saved subscriptions")
		}
		for _, sub := range state.Subscriptions {
			for _, node := range sub.Nodes {
				fmt.Printf("%s\t%s\t%s:%d\n", node.ID, node.Name, node.Server, node.Port)
			}
		}
		return nil
	case "refresh":
		data, err := os.ReadFile("/run/xray-manager.pid")
		if err != nil {
			return errors.New("manager is not running")
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return errors.New("invalid manager PID")
		}
		return syscall.Kill(pid, syscall.SIGHUP)
	case "serve":
	default:
		return errors.New("usage: xray-manager [serve|list|refresh]")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	state, err := store.LoadOrImport(os.Getenv)
	if err != nil {
		return fmt.Errorf("could not load settings: %w", err)
	}
	auth, generated, err := app.OpenAuth(dir, os.Getenv("PANEL_PASSWORD"))
	if err != nil {
		return err
	}
	// The initial password is printed only once, outside the browser-visible log buffer.
	if generated != "" {
		fmt.Printf("Panel initial password: %s\n", generated)
	}
	logs := app.NewLogBuffer(os.Stdout)
	network := &app.LinuxRuntime{}
	setupCtx, setupCancel := context.WithTimeout(ctx, 20*time.Second)
	err = network.Setup(setupCtx)
	setupCancel()
	if err != nil {
		return err
	}
	runner := app.NewRunner(env("XRAY_BINARY", "/opt/xray/xray"), dir, logs, time.Second)
	defer runner.Stop()
	manager := app.NewManager(store, state, network, runner, logs)
	coreVersion, versionErr := app.DetectXrayVersion(ctx, env("XRAY_BINARY", "/opt/xray/xray"))
	if versionErr != nil {
		logs.Add("Xray version unavailable")
	} else {
		manager.SetCoreVersion(coreVersion)
	}
	manager.SetUserAgent(os.Getenv("SUBSCRIPTION_USER_AGENT"))
	manager.SetProber(&app.XrayProber{Binary: env("XRAY_BINARY", "/opt/xray/xray"), Dir: dir, URL: os.Getenv("PROBE_URL"), Network: network})
	address := env("PANEL_ADDRESS", "0.0.0.0:8080")
	listener, err := network.Listen(ctx, address)
	if err != nil {
		return errors.New("could not open panel port; check PANEL_ADDRESS")
	}
	server := &http.Server{Handler: app.NewServer(manager, auth), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, BaseContext: func(net.Listener) context.Context { return ctx }}
	if err = os.WriteFile("/run/xray-manager.pid", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		_ = listener.Close()
		return errors.New("could not save manager PID")
	}
	defer os.Remove("/run/xray-manager.pid")
	logs.Add("Control panel listening on " + address)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	var workers sync.WaitGroup
	workers.Go(func() { _ = manager.Bootstrap(ctx); manager.RunScheduler(ctx) })
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	workers.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				manager.RefreshAll(ctx)
			}
		}
	})
	select {
	case <-ctx.Done():
	case err = <-serveErrors:
		cancel()
		if !errors.Is(err, http.ErrServerClosed) {
			logs.Add("HTTP server stopped")
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	workers.Wait()
	runner.Stop()
	logs.Add("Manager stopped")
	// Keep accepted state private; no dump of credentials or URLs on shutdown.
	return nil
}
