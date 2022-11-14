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

	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/lightbitslabs/discovery-client/pkg/nvmeclient"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newConnectAllCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "connect-all",
		Short:             "Discover NVMeoF subsystems and connect to them",
		Long:              ``,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		RunE:              connectAllCmdFunc,
	}

	cmd.Flags().StringP("traddr", "a", "", "traddr")
	viper.BindPFlag("connect-all.traddr", cmd.Flags().Lookup("traddr"))

	cmd.Flags().IntP("trsvcid", "s", 8009, "trsvcid")
	viper.BindPFlag("connect-all.trsvcid", cmd.Flags().Lookup("trsvcid"))

	cmd.Flags().StringP("hostnqn", "q", "", "hostnqn")
	viper.BindPFlag("connect-all.hostnqn", cmd.Flags().Lookup("hostnqn"))

	cmd.Flags().StringP("transport", "t", "tcp", "trtype")
	viper.BindPFlag("connect-all.transport", cmd.Flags().Lookup("transport"))

	cmd.Flags().StringP("host-traddr", "w", "", "host-traddr")
	viper.BindPFlag("connect-all.host-traddr", cmd.Flags().Lookup("host-traddr"))

	cmd.Flags().BoolP("persistant", "p", false, "persistant")
	viper.BindPFlag("connect-all.persistant", cmd.Flags().Lookup("persistant"))

	cmd.Flags().IntP("max-queues", "m", 0, "max-queues")
	viper.BindPFlag("connect-all.max-queues", cmd.Flags().Lookup("max-queues"))

	return cmd
}

func connectAllCmdFunc(cmd *cobra.Command, args []string) error {
	if !viper.IsSet("connect-all.traddr") {
		return fmt.Errorf("traddr(-a) must be set")
	}

	entry := &hostapi.DiscoverRequest{
		Traddr:    viper.GetString("connect-all.traddr"),
		Trsvcid:   viper.GetInt("connect-all.trsvcid"),
		Kato:      kato,
		Hostnqn:   viper.GetString("connect-all.hostnqn"),
		Transport: viper.GetString("connect-all.transport"),
	}
	ctrls, err := nvmeclient.ConnectAll(entry, viper.GetInt("connect-all.max-queues"))
	if err != nil {
		return err
	}
	if err := print(ctrls, JSON); err != nil {
		return err
	}

	return nil
}
