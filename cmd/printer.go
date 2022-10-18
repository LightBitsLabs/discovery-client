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
