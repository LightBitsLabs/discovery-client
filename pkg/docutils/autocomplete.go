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

package docutils

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

var autocompleteTarget string

// bash for now (zsh and others will come)
var autocompleteType string

func NewAutocompleteCmd(applicationName string) *cobra.Command {
	short := fmt.Sprintf("Generate shell autocompletion script for %s", applicationName)
	long := fmt.Sprintf(`Generates a shell autocompletion script for %s.
NOTE: The current version supports Bash only.
This should work for *nix systems with Bash installed.
By default, the file is written directly to /etc/bash_completion.d
for convenience, and the command may need superuser rights, e.g.:
	$ sudo %s gen autocomplete
Add "--completionfile=/path/to/file" flag to set alternative
file-path and name.
Logout and in again to reload the completion scripts,
or just source them in directly:
	$ . /etc/bash_completion`, applicationName, applicationName)
	example := fmt.Sprintf(`# Create a bash completion file
%s gen autocomplete --completionfile=/path/to/file`, applicationName)

	cmd := &cobra.Command{
		Use:               "autocomplete",
		Short:             short,
		DisableAutoGenTag: true,
		Long:              long,
		Example:           example,
		RunE:              autocompleteCmdFunc,
	}

	cmd.PersistentFlags().StringVarP(&autocompleteTarget, "completionfile", "", fmt.Sprintf("/etc/bash_completion.d/%s.sh", applicationName), "Auto completion file")
	cmd.PersistentFlags().StringVarP(&autocompleteType, "type", "", "bash", "Auto complete file type (currently only bash supported)")

	// For bash-completion
	cmd.PersistentFlags().SetAnnotation("completionfile", cobra.BashCompFilenameExt, []string{})
	return cmd
}

func autocompleteCmdFunc(cmd *cobra.Command, args []string) error {
	if autocompleteType != "bash" {
		return fmt.Errorf("Only Bash is supported for now")
	}

	err := cmd.Root().GenBashCompletionFile(autocompleteTarget)
	if err != nil {
		return err
	}

	log.Println("Bash completion file saved to: ", autocompleteTarget)

	return nil
}
