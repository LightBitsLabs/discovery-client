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
)

func newDisconnectAllCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "disconnect-all",
		Short:             "Disconnect from all connected NVMeof subsystems",
		Long:              ``,
		DisableAutoGenTag: true,
		RunE:              disconnectAllCmdFunc,
	}

	return cmd
}

func disconnectAllCmdFunc(cmd *cobra.Command, args []string) error {
	controllerIdentifiers, err := nvmeclient.ListNvmeControllersInfo()
	if err != nil {
		return err
	}
	for _, controllerIdentifier := range controllerIdentifiers {
		if exists, err := nvmeclient.CheckCtrlRemovePathExists(controllerIdentifier.Device); err != nil || !exists {
			if err != nil {
				fmt.Printf("failed to check if ctrl remove path exists: %q. err: %v\n", controllerIdentifier.Device, err)
			}
			continue
		}

		err := nvmeclient.RemoveCtrlByDevice(controllerIdentifier.Device)
		if err != nil {
			fmt.Print("failed to disconnect device: %q", controllerIdentifier.Device)
		}
	}
	return nil
}
