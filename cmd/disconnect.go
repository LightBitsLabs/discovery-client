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
