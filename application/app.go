// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package application

import (
	"context"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/daemon"
	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/service"
	"github.com/lightbitslabs/discovery-client/pkg/clientconfig"
	"github.com/lightbitslabs/discovery-client/pkg/nvme/nvmehost"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// App - the main application
type App struct {
	cfg    *model.AppConfig
	cache  clientconfig.Cache
	svc    service.Service
	ctx    context.Context
	cancel context.CancelFunc
	log    *logrus.Entry
}

// NewApp - Returns an App instance.
func NewApp(cfg *model.AppConfig) (*App, error) {
	app := &App{
		log: logrus.WithFields(logrus.Fields{}),
		cfg: cfg,
	}
	return app, nil
}

// Start - bootstrap the application components
func (app *App) Start() error {
	app.ctx, app.cancel = context.WithCancel(context.Background())

	app.handleSignals()

	// run a webserver to get the pprof webserver
	go func() {
		if len(app.cfg.Debug.Endpoint) > 0 {
			http.Handle("/metrics", promhttp.Handler())
			app.log.Infof("%v", http.ListenAndServe(app.cfg.Debug.Endpoint, nil))
		}
	}()

	dirs := make([]string, 2)
	dirs[0] = app.cfg.ClientConfigDir
	dirs[1] = app.cfg.InternalDir
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			app.log.Warnf("folder %q does not exists. creating it", dir)
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return err
			}
		}
	}
	app.cache = clientconfig.NewCache(app.ctx, app.cfg.ClientConfigDir, app.cfg.InternalDir, &app.cfg.AutoDetectEntries)
	hostAPI := nvmehost.NewHostApi(app.cfg.LogPagePaginationEnabled)
	app.svc = service.NewService(app.ctx, app.cache, hostAPI, app.cfg.ReconnectInterval, app.cfg.MaxIOQueues)
	if err := app.svc.Start(); err != nil {
		return err
	}
	daemon.SdNotify(false, "READY=1")
	// this is the main loop of the application.
	// it will run until the stop is called for the application.
	// it will monitor health status reported by the Provider.
	// in case of NOT_SERVING it will stop the tcpServer and will stop responding for NVMe Requests
	// if it is back to SERVING it will restart the tcpServer and will accept connections.
	alive := true
	for alive {
		select {
		case <-app.ctx.Done():
			app.log.Info("aborted main loop")
			alive = false
		}
	}
	return nil
}

// Stop terminates the Server and performs any necessary finalization.
func (app *App) Stop() error {
	app.log.Info("shutting down.")
	if app.cancel != nil {
		app.cancel()
		app.cancel = nil
	}
	app.cache.Stop()
	return nil
}

func (app *App) handleSignals() chan bool {
	// After setting everything up!
	// Wait for a SIGINT (perhaps triggered by user with CTRL-C)
	// Run cleanup when signal is received
	signalChan := make(chan os.Signal, 1)
	cleanupDone := make(chan bool)
	signal.Notify(signalChan, os.Interrupt, os.Kill, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		sig := <-signalChan
		switch sig {
		case syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT:
			app.log.Infof("Received an \"%v\" signal, stopping services...", sig)
			app.Stop()
		default:
			app.log.Infof("Received a \"%v\" signal. ignoring...", sig)
		}
	}()
	return cleanupDone
}
