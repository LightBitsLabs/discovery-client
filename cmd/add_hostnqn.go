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
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/pkg/clientconfig"
)

type output struct {
	File string `json:"name"`
}

func newAddHostNqnCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:               "add-hostnqn",
		Short:             "Add hostnqn",
		DisableAutoGenTag: true,
		RunE:              addHostNqnCmdFunc,
	}

	cmd.Flags().StringP("name", "", "", fmt.Sprintf("name of the file to create. can't contain prefix: %q", model.DiscoveryClientReservedPrefix))
	cmd.Flags().StringSliceP("addresses", "a", []string{}, "endpoints of discovery services. format: <hostname|ip-address>:<port>")
	cmd.Flags().StringP("hostnqn", "q", "", "host nqn")
	cmd.Flags().StringP("nqn", "n", "", "subsystem nqn")
	cmd.Flags().StringP("transport", "t", "tcp", "transport name - default to tcp")

	return cmd
}

func addHostNqnCmdFunc(cmd *cobra.Command, args []string) error {
	appConfig, err := model.LoadFromViper()
	if err != nil {
		return err
	}

	if !cmd.Flags().Changed("name") {
		return fmt.Errorf("name must be set")
	}

	if !cmd.Flags().Changed("addresses") {
		return fmt.Errorf("addresses(-a) must be set")
	}

	if !cmd.Flags().Changed("hostnqn") {
		return fmt.Errorf("hostnqn(-q) must be set")
	}

	if !cmd.Flags().Changed("nqn") {
		return fmt.Errorf("nqn(-n) must be set")
	}

	if _, err := os.Stat(appConfig.ClientConfigDir); os.IsNotExist(err) {
		if err := os.MkdirAll(appConfig.ClientConfigDir, os.ModePerm); err != nil {
			return err
		}
	}
	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("failed to get 'name' value, %w", err)
	}
	if strings.HasPrefix(name, model.DiscoveryClientReservedPrefix) {
		return fmt.Errorf("name can't start with prefix: %q", model.DiscoveryClientReservedPrefix)
	}
	hostnqn, err := cmd.Flags().GetString("hostnqn")
	if err != nil {
		return fmt.Errorf("failed to get 'hostnqn' value, %w", err)
	}
	nqn, err := cmd.Flags().GetString("nqn")
	if err != nil {
		return fmt.Errorf("failed to get 'nqn' value, %w", err)
	}
	transport, err := cmd.Flags().GetString("transport")
	if err != nil {
		return fmt.Errorf("failed to get 'transport' value, %w", err)
	}
	addresses, err := cmd.Flags().GetStringSlice("addresses")
	if err != nil {
		return fmt.Errorf("failed to get 'addresses' value, %w", err)
	}

	entries, err := clientconfig.CreateEntries(addresses, hostnqn, nqn, transport)
	if err != nil {
		return err
	}
	filename := path.Join(appConfig.ClientConfigDir, name)
	if err := clientconfig.CreateFile(filename, entries); err != nil {
		return err
	}

	print(&output{File: filename}, JSON)
	return nil
}
