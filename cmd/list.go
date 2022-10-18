package cmd

import (
	"github.com/lightbitslabs/discovery-client/pkg/nvmeclient"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "list",
		Short:             "List NVMe resources",
		Long:              ``,
		DisableAutoGenTag: true,
	}
	cmd.AddCommand(newListCtrlCmd())

	return cmd
}

func newListCtrlCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "ctrl",
		Short:             "List NVMe controllers",
		Long:              ``,
		DisableAutoGenTag: true,
		RunE:              listCtrlCmdFunc,
	}

	cmd.Flags().BoolP("discovery", "d", false, "list only discovery controllers")

	return cmd
}

func listCtrlCmdFunc(cmd *cobra.Command, args []string) error {
	info, err := nvmeclient.ListNvmeControllersInfo()
	if err != nil {
		return err
	}

	if err := print(info, JSON); err != nil {
		return err
	}
	return nil
}
