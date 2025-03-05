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

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/lightbitslabs/discovery-client/pkg/nvme/nvmehost"
)

func newDiscoverCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "discover",
		Short:             "Issue NVMe/TCP discover command",
		Long:              ``,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		RunE:              discoverCmdFunc,
	}

	cmd.Flags().StringP("traddr", "a", "", "traddr")
	viper.BindPFlag("discover.traddr", cmd.Flags().Lookup("traddr"))

	cmd.Flags().IntP("trsvcid", "s", 8009, "trsvcid")
	viper.BindPFlag("discover.trsvcid", cmd.Flags().Lookup("trsvcid"))

	cmd.Flags().StringP("hostnqn", "q", "", "hostnqn")
	viper.BindPFlag("discover.hostnqn", cmd.Flags().Lookup("hostnqn"))

	cmd.Flags().StringP("hostid", "I", "", "user-defined hostid (if default not used)")
	viper.BindPFlag("discover.hostid", cmd.Flags().Lookup("hostid"))

	cmd.Flags().StringP("transport", "t", "tcp", "trtype")
	viper.BindPFlag("discover.transport", cmd.Flags().Lookup("transport"))

	cmd.Flags().StringP("host-traddr", "w", "", "host-traddr")
	viper.BindPFlag("discover.host-traddr", cmd.Flags().Lookup("host-traddr"))

	cmd.Flags().BoolP("persistant", "p", false, "persistant")
	viper.BindPFlag("discover.persistant", cmd.Flags().Lookup("persistant"))

	return cmd
}

func discoverCmdFunc(cmd *cobra.Command, args []string) error {
	if !viper.IsSet("discover.traddr") {
		return fmt.Errorf("traddr(-a) must be set")
	}
	if !viper.IsSet("discover.hostnqn") {
		return fmt.Errorf("hostnqn(-q) must be set")
	}
	katoValue := kato
	if viper.GetBool("discover.persistent") == false {
		katoValue = 0
	}
	entry := &hostapi.DiscoverRequest{
		Traddr:    viper.GetString("discover.traddr"),
		Trsvcid:   viper.GetInt("discover.trsvcid"),
		Kato:      katoValue,
		Hostnqn:   viper.GetString("discover.hostnqn"),
		Hostid:    viper.GetString("discover.hostid"),
		Transport: viper.GetString("discover.transport"),
	}

	hostAPI := nvmehost.NewHostApi(true, model.DefaultHostIDPath)
	logPageEntries, _, err := hostAPI.Discover(entry)
	if err != nil {
		return err
	}

	if err := print(logPageEntries, JSON); err != nil {
		return err
	}

	return nil
}
