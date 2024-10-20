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

package model

import (
	"fmt"
	"os"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/logging"
	"github.com/spf13/viper"
)

const (
	DiscoveryClientReservedPrefix = "tmp.dc."
)

type DebugInfo struct {
	Endpoint    string `yaml:"endpoint,omitempty"`
	Enablepprof bool   `yaml:"enablepprof,omitempty"`
	Metrics     bool   `yaml:"metrics,omitempty"`
}

type AutoDetectEntries struct {
	Enabled              bool   `yaml:"enabled,omitempty"`
	Filename             string `yaml:"filename,omitempty"`
	DiscoveryServicePort uint32 `yaml:"discoveryServicePort,omitempty"`
}

// AppConfig application configuration
type AppConfig struct {
	Cores                    []int             `yaml:"cores,omitempty"`
	Logging                  logging.Config    `yaml:"logging,omitempty"`
	ClientConfigDir          string            `yaml:"clientConfigDir,omitempty"`
	Debug                    DebugInfo         `yaml:"debug,omitempty"`
	ReconnectInterval        time.Duration     `yaml:"reconnectInterval,omitempty"`
	InternalDir              string            `yaml:"internalDir,omitempty"`
	LogPagePaginationEnabled bool              `yaml:"logPagePaginationEnabled"`
	MaxIOQueues              int               `yaml:"maxIOQueues"`
	AutoDetectEntries        AutoDetectEntries `yaml:"autoDetectEntries,omitempty"`
	NvmeHostIDPath           string            `yaml:"nvmeHostIDPath,omitempty"`
	Kato					 int 			   `yaml:"kato"`
}

func (cfg *AppConfig) verifyConfigurationIsValid() error {
	if cfg.ClientConfigDir == cfg.InternalDir {
		return fmt.Errorf("Internal dir identical to ClientConfigDir: %q", cfg.ClientConfigDir)
	}
	return cfg.Logging.IsValid()
}

// LoadFromViper use viper package to load configuration from file cmd line and env.
func LoadFromViper() (*AppConfig, error) {
	appConfig := &AppConfig{}
	if err := viper.Unmarshal(&appConfig); err != nil {
		fmt.Fprintf(os.Stderr, "unable to decode into struct. error: %v", err)
		return nil, err
	}
	if err := appConfig.verifyConfigurationIsValid(); err != nil {
		return nil, err
	}
	return appConfig, nil
}
