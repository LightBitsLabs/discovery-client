package testutils

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func CreateTempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "example")
	require.NoError(t, err, "failed to create temp dir")
	return dir
}

func CreateFile(t *testing.T, filename string, content string) {
	err := ioutil.WriteFile(filename, []byte(content), 0666)
	require.NoError(t, err, "failed to write file")
}

func DeleteFile(t *testing.T, filename string) {
	t.Logf("Removing %s", filename)
	err := os.Remove(filename)
	require.NoError(t, err, "failed to remove file")
}
