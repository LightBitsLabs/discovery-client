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
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v2"
)

type outputFormat string

const (
	JSON outputFormat = "json"
	YAML outputFormat = "yaml"
)

func print(val interface{}, of outputFormat) error {
	switch of {
	case JSON:
		return printJson(val)
	case YAML:
		return printYaml(val)
	default:
		return printJson(val)
	}
	return nil
}

func printJson(val interface{}) error {
	b, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshal. error: %v", err)
	}
	fmt.Println(string(b))
	return nil
}

func printYaml(val interface{}) error {
	b, err := yaml.Marshal(val)
	if err != nil {
		return fmt.Errorf("failed marshal. error: %v", err)
	}
	fmt.Print(string(b))
	return nil
}
