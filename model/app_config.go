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
