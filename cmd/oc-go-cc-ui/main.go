package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oc-go-cc/internal/controlpanel"
)

var version = "dev"

func main() {
	var configPath string
	var listenAddr string
	var serviceBinary string
	var servicePort int
	var noOpen bool
	var idleTimeout time.Duration

	flag.StringVar(&configPath, "config", "", "Path to oc-go-cc config file")
	flag.StringVar(&listenAddr, "listen", "127.0.0.1:0", "Address for the control panel UI")
	flag.StringVar(&serviceBinary, "service-binary", "", "Path to the oc-go-cc service binary")
	flag.IntVar(&servicePort, "port", 0, "Optional port override for starting the service")
	flag.BoolVar(&noOpen, "no-open", false, "Do not open the browser automatically")
	flag.DurationVar(&idleTimeout, "idle-timeout", 2*time.Minute, "Close the control panel after this much inactivity")
	flag.Parse()

	app, err := controlpanel.New(version, serviceBinary, configPath, servicePort)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	server := &http.Server{
		Handler:      app.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	shutdown := func() {
		ctx, cancel := controlpanel.WithShutdownTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	app.SetQuitFunc(shutdown)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	if !noOpen {
		_ = controlpanel.OpenBrowser(url)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	idleTicker := time.NewTicker(15 * time.Second)
	defer idleTicker.Stop()

	for {
		select {
		case <-sigCtx.Done():
			shutdown()
			return
		case <-idleTicker.C:
			if idleTimeout > 0 && time.Since(app.LastAccess()) > idleTimeout {
				shutdown()
				return
			}
		case err := <-errCh:
			if err != nil && err != http.ErrServerClosed {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
}
