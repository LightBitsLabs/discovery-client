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

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/lightbitslabs/discovery-client/application"
	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/pkg/logging"
	"github.com/lightbitslabs/discovery-client/pkg/processutil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newServeCmd() *cobra.Command {

	var cmd = &cobra.Command{
		Use:               "serve",
		Short:             "Start NVMeOF Discovery Client",
		Long:              ``,
		DisableAutoGenTag: true,
		RunE:              serveCmdFunc,
	}

	// configure logging
	cmd.Flags().String("logging.filename", "", "filename to write log to")
	viper.BindPFlag("logging.filename", cmd.Flags().Lookup("logging.filename"))
	cmd.MarkFlagFilename("logging.filename", "log")

	cmd.Flags().Duration("logging.maxage", 96*time.Hour, "Time to wait until old logs are purged")
	viper.BindPFlag("logging.maxage", cmd.Flags().Lookup("logging.maxage"))

	cmd.Flags().Int("logging.maxSize", 100, "Maximum size in megabytes of the log file before it gets rotated. (defaults to 100MB).")
	viper.BindPFlag("logging.maxSize", cmd.Flags().Lookup("logging.maxSize"))

	cmd.Flags().Bool("logging.reportcaller", true, "Report func name and line number on log entry")
	viper.BindPFlag("logging.reportcaller", cmd.Flags().Lookup("logging.reportcaller"))

	cmd.Flags().String("logging.level", "debug", "Log level we support")
	viper.BindPFlag("logging.level", cmd.Flags().Lookup("logging.level"))

	cmd.Flags().String("debug.endpoint", "0.0.0.0:6060", "ip:port to expose debug and metric information")
	viper.BindPFlag("debug.endpoint", cmd.Flags().Lookup("debug.endpoint"))

	cmd.Flags().Bool("debug.enablepprof", true, "Enable runtime profiling data via HTTP server. http://<endpoint>/debug/pprof/")
	viper.BindPFlag("debug.enablepprof", cmd.Flags().Lookup("debug.enablepprof"))

	cmd.Flags().Bool("debug.metrics", true, "Expose prometheus metrics on http://<endpoint>/metrics")
	viper.BindPFlag("debug.metrics", cmd.Flags().Lookup("debug.metrics"))

	cmd.Flags().String("clientConfigDir", "/etc/discovery-client/discovery.d", "Directory to watch for discovery service configurations")
	viper.BindPFlag("clientConfigDir", cmd.Flags().Lookup("clientConfigDir"))

	cmd.Flags().String("internalDir", "/etc/discovery-client/internal", "Directory to store internal cache")
	viper.BindPFlag("internalDir", cmd.Flags().Lookup("internalDir"))

	cmd.Flags().String("nvmeHostIDPath", "/etc/nvme/hostid", "file path containing nvme host id")
	viper.BindPFlag("nvmeHostIDPath", cmd.Flags().Lookup("nvmeHostIDPath"))

	cmd.Flags().Duration("pollingInterval", 5*time.Second, "Polling interval for querying the discovery service.")
	viper.BindPFlag("pollingInterval", cmd.Flags().Lookup("pollingInterval"))

	cmd.Flags().Int("maxIOQueues", 0, "Overrides the default number of I/O queues create by the driver. Zero value means no override (default driver value is number of cores).")
	viper.BindPFlag("maxIOQueues", cmd.Flags().Lookup("maxIOQueues"))

	cmd.Flags().Int("kato", 10, "Host keep alive time out")
	viper.BindPFlag("kato", cmd.Flags().Lookup("kato"))

	// auto detect configuration
	cmd.Flags().BoolP("autoDetectEntries.enabled", "e", true, "should we detect")
	viper.BindPFlag("autoDetectEntries.enabled", cmd.Flags().Lookup("autoDetectEntries.enabled"))
	cmd.Flags().StringP("autoDetectEntries.filename", "f", "detected-io-controllers", "name of the file we want to store this information")
	viper.BindPFlag("autoDetectEntries.filename", cmd.Flags().Lookup("autoDetectEntries.filename"))
	cmd.Flags().UintP("autoDetectEntries.discoveryServicePort", "p", 8009, "discovery-service port")
	viper.BindPFlag("autoDetectEntries.discoveryServicePort", cmd.Flags().Lookup("autoDetectEntries.discoveryServicePort"))
	return cmd
}

func serveCmdFunc(cmd *cobra.Command, args []string) error {
	appConfig, err := model.LoadFromViper()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "discovery-client configuration: %#v\n", *appConfig)

	logging.SetupLogging(appConfig.Logging)
	logrus.Infof("******************** %s started ********************", os.Args[0])

	if err = processutil.SetupCPUAffinity(appConfig.Cores); err != nil {
		logrus.WithError(err).Errorf("failed to set cpu affinity")
		return err
	}
	app, err := application.NewApp(appConfig)
	if err != nil {
		logrus.WithError(err).Errorf("failed to create new application")
		return err
	}
	if err = app.Start(); err != nil {
		logrus.WithError(err).Errorf("failed to start application")
	}
	return err
}
