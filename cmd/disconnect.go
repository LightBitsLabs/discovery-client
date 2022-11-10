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

func newDisconnectCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "disconnect",
		Short:             "Issue NVMe/TCP disconnect command",
		Long:              ``,
		DisableAutoGenTag: true,
		RunE:              disconnectCmdFunc,
	}

	cmd.Flags().StringP("device", "d", "", "nvme device")
	viper.BindPFlag("device", cmd.Flags().Lookup("device"))

	return cmd
}

func disconnectCmdFunc(cmd *cobra.Command, args []string) error {
	if !viper.IsSet("device") {
		return fmt.Errorf("device must be set")
	}
	device := viper.GetString("device")

	err := nvmeclient.RemoveCtrlByDevice(device)
	if err != nil {
		msg := fmt.Errorf("disconnect device %q failed: %s", device, err)
		fmt.Printf("%v\n", msg)
		return msg
	}
	return nil
}
