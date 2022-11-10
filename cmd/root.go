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
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/docutils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	applicationName string
	cfgFile         string
)

const kato = time.Duration(0) // We do not use persistent connections when running cli commands

func init() {
	cobra.OnInitialize(initConfig)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	viperLoadConfig(cfgFile)
}

func init() {
	applicationName = path.Base(os.Args[0])
}

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "discovery-client",
		Short:             "NVMe/TCP Discovery Client",
		Long:              ``,
		DisableAutoGenTag: true,
	}
	cmd.AddCommand(
		docutils.NewGenCmd(applicationName),
		newServeCmd(),
		newDiscoverCmd(),
		newConnectCmd(),
		newConnectAllCmd(),
		newDisconnectCmd(),
		newDisconnectAllCmd(),
		newListCmd(),
		newAddHostNqnCmd(),
		newRemoveHostNqnCmd(),
	)

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.discovery-client/discovery-client.yaml)")
	cmd.MarkFlagFilename("config", "yaml", "yml")

	return cmd
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	defer func() {
		if err := recover(); err != nil {
			logrus.Errorf("start got panic: %s\n%s", err, debug.Stack())
			os.Exit(-2)
		}
	}()

	rootCmd := NewRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(-1)
	}
}

func viperLoadConfig(configFile string) {
	if configFile != "" { // enable ability to specify config file via flag
		viper.SetConfigFile(configFile)
	} else {
		viper.SetConfigType("yaml")
		viper.SetConfigName("discovery-client")       // name of config file (without extension)
		viper.AddConfigPath("./etc/discovery-client") // adding home directory as first search path
		viper.AddConfigPath("/etc/discovery-client/") // path to look for the config file in 3rd search path
		viper.AutomaticEnv()                          // read in environment variables that match
		viper.SetEnvPrefix("dc")
		viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	}

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("%v\n", err)
	}
}
