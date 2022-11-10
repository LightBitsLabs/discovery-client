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
