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
	"testing"

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
				Cores:           []int{0},
				ClientConfigDir: `/etc/discovery-client/discovery.d/`,
				InternalDir:     `/etc/discovery-client/internal/`,
			},
			err: nil,
		},
		{
			name: "identical internal and client directory",
			appConfig: &AppConfig{
				Cores:           []int{0},
				ClientConfigDir: `/etc/discovery-client/discovery.d/`,
				InternalDir:     `/etc/discovery-client/discovery.d/`,
			},
			err: fmt.Errorf("Internal dir identical to ClientConfigDir: %q", `/etc/discovery-client/discovery.d/`),
		},
		{
			name: "illeagle log level",
			appConfig: &AppConfig{
				Cores:           []int{0},
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
