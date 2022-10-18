package cmd

import (
	"fmt"
	"path/filepath"
	"os"

	"github.com/lightbitslabs/discovery-client/model"
	"github.com/spf13/cobra"
)

func newRemoveHostNqnCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "remove-hostnqn",
		Short:             "Remove hostnqn",
		DisableAutoGenTag: true,
		RunE:              removeHostNqnCmdFunc,
	}

	cmd.Flags().StringP("name", "n", "", "name of the file to delete")

	return cmd
}

func removeHostNqnCmdFunc(cmd *cobra.Command, args []string) error {
	appConfig, err := model.LoadFromViper()
	if err != nil {
		return err
	}

	if !cmd.Flags().Changed("name") {
		return fmt.Errorf("name(-n) must be set")
	}

	name, err := cmd.Flags().GetString("name")

	filename := filepath.Join(appConfig.ClientConfigDir, name)

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil
	}

	if err := os.RemoveAll(filename); err != nil {
		return err
	}
	print(&output{File: filename}, JSON)
	return nil
}
