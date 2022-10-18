package model

import (
	"fmt"
	"testing"

	"github.com/lightbitslabs/discovery-client/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestAppConfig(t *testing.T) {
	testCases := []struct {
		name      string
		appConfig *AppConfig
		err       error
	}{
		{
			name: "valid",
			appConfig: &AppConfig{
				Cores: []int{0},
				Logging: logging.Config{
					Level: "debug",
				},
				ClientConfigDir: `/etc/discovery-client/discovery.d/`,
				InternalDir:     `/etc/discovery-client/internal/`,
			},
			err: nil,
		},
		{
			name: "identical internal and client directory",
			appConfig: &AppConfig{
				Cores: []int{0},
				Logging: logging.Config{
					Level: "debug",
				},
				ClientConfigDir: `/etc/discovery-client/discovery.d/`,
				InternalDir:     `/etc/discovery-client/discovery.d/`,
			},
			err: fmt.Errorf("Internal dir identical to ClientConfigDir: %q", `/etc/discovery-client/discovery.d/`),
		},
		{
			name: "illeagle log level",
			appConfig: &AppConfig{
				Cores: []int{0},
				Logging: logging.Config{
					Level: "wrong_level",
				},
				ClientConfigDir: `/etc/discovery-client/discovery.d/`,
				InternalDir:     `/etc/discovery-client/internal/`,
			},
			err: fmt.Errorf("invalid logging.level parameter provided. supported levels: [debug info warn warning error fatal], provided: wrong_level"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.appConfig.verifyConfigurationIsValid()
			if tc.err != nil {
				require.EqualErrorf(t, err, tc.err.Error(), "errors don't match")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
