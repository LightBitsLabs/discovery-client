package docutils

import (
	"github.com/spf13/cobra"
)

func NewGenCmd(applicationName string) *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "gen",
		Short:             `Internal use only`,
		Long:              `Internal use only`,
		DisableAutoGenTag: true,
	}
	cmd.AddCommand(NewGenDocCmd(applicationName))
	cmd.AddCommand(NewAutocompleteCmd(applicationName))
	return cmd
}
