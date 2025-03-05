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

	"github.com/lightbitslabs/discovery-client/pkg/nvmeclient"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newConnectCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "connect",
		Short: "Issue NVMe/TCP connect command",
		Long: `Create a transport connection to a remote system (specified by --traddr and
--trsvcid) and create a NVMe over Fabrics controller for the NVMe subsystem
specified by the --nqn option.`,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		RunE:              connectCmdFunc,
	}

	cmd.Flags().StringP("traddr", "a", "", "traddr")
	viper.BindPFlag("connect.traddr", cmd.Flags().Lookup("traddr"))

	cmd.Flags().IntP("trsvcid", "s", 4420, "trsvcid")
	viper.BindPFlag("connect.trsvcid", cmd.Flags().Lookup("trsvcid"))

	cmd.Flags().StringP("hostnqn", "q", "", "hostnqn")
	viper.BindPFlag("connect.hostnqn", cmd.Flags().Lookup("hostnqn"))

	cmd.Flags().StringP("hostid", "I", "", "user-defined hostid (if default not used)")
	viper.BindPFlag("connect.hostid", cmd.Flags().Lookup("hostid"))

	cmd.Flags().StringP("transport", "t", "tcp", "transport type")
	viper.BindPFlag("connect.transport", cmd.Flags().Lookup("transport"))

	cmd.Flags().StringP("host-traddr", "w", "", "host-traddr")
	viper.BindPFlag("connect.host-traddr", cmd.Flags().Lookup("host-traddr"))

	cmd.Flags().StringP("nqn", "n", "", "nqn")
	viper.BindPFlag("connect.nqn", cmd.Flags().Lookup("nqn"))

	cmd.Flags().IntP("ctrl-loss-tmo", "", -1, "controller loss timeout period (in seconds). Timeout is disabled by default (-1)")
	viper.BindPFlag("connect.ctrl-loss-tmo", cmd.Flags().Lookup("ctrl-loss-tmo"))

	return cmd
}

func connectCmdFunc(cmd *cobra.Command, args []string) error {
	if !viper.IsSet("connect.traddr") {
		return fmt.Errorf("traddr(-a) must be set")
	}
	if !viper.IsSet("connect.nqn") {
		return fmt.Errorf("nqn(-n) must be set")
	}

	request := &nvmeclient.ConnectRequest{
		Traddr:      viper.GetString("connect.traddr"),
		Trsvcid:     viper.GetInt("connect.trsvcid"),
		Subsysnqn:   viper.GetString("connect.nqn"),
		Hostnqn:     viper.GetString("connect.hostnqn"),
		Hostid:      viper.GetString("connect.hostid"),
		Transport:   viper.GetString("connect.transport"),
		CtrlLossTMO: viper.GetInt("connect.ctrl-loss-tmo"),
	}
	ctrlID, err := nvmeclient.Connect(request)
	if err != nil {
		return err
	}

	if err := print(ctrlID, JSON); err != nil {
		return err
	}

	return nil
}
