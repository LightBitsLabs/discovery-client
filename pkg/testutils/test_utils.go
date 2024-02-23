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

package testutils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func CreateTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "example")
	require.NoError(t, err, "failed to create temp dir")
	return dir
}

func CreateFile(t *testing.T, filename string, content string) {
	err := os.WriteFile(filename, []byte(content), 0666)
	require.NoError(t, err, "failed to write file")
}

func DeleteFile(t *testing.T, filename string) {
	t.Logf("Removing %s", filename)
	err := os.Remove(filename)
	require.NoError(t, err, "failed to remove file")
}
