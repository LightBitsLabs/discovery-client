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
