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

package clientconfig

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/pkg/commonstructs"
	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/lightbitslabs/discovery-client/pkg/regexutil"
	"github.com/sirupsen/logrus"
)

var (
	addressRegex = regexp.MustCompile(`^traddr=(?P<traddr>[^,]+),trsvcid=(?P<trsvcid>\d+)$`)
	NvmeCtrlPath = filepath.Join("/sys/class/nvme", "nvme[0-9]")
)

func CreateEntries(addresses []string, hostnqn string, nqn string, transport string) ([]*commonstructs.Entry, error) {
	var entries []*commonstructs.Entry
	for _, address := range addresses {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}

		_, err = nvme.AdjustTraddr(host)
		if err != nil {
			return nil, err
		}

		port_str, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return nil, err
		}
		e := &commonstructs.Entry{
			Transport: transport,
			Traddr:    host,
			Trsvcid:   int(port_str),
			Hostnqn:   hostnqn,
			Nqn:       nqn,
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func CreateFile(filename string, entries []*commonstructs.Entry) error {
	folder := path.Dir(filename)
	content := []byte(commonstructs.EntriesToString(entries))
	tmpfile, err := os.CreateTemp(folder, model.DiscoveryClientReservedPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name()) // clean up

	if _, err := tmpfile.Write(content); err != nil {
		return err
	}
	if err := tmpfile.Close(); err != nil {
		return err
	}

	err = os.Rename(tmpfile.Name(), filename)
	if err != nil {
		return err
	}
	return nil
}

// ShouldGenerateAutoDetectedEntries - return true only in case client and internal dirs are empty
func ShouldGenerateAutoDetectedEntries(clientConfigDir, internalDir string) bool {
	clientConfigDirEmpty, clientConfigDirErr := isDirectoryEmpty(clientConfigDir)
	internalDirEmpty, internalDirErr := isDirectoryEmpty(internalDir)
	return (clientConfigDirErr != nil || clientConfigDirEmpty) && (internalDirErr != nil || internalDirEmpty)
}

func isDirectoryEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1) // Or f.Readdir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err // Either not empty or error, suits both cases
}

func StoreEntries(filename string, entries []*commonstructs.Entry) error {
	if err := CreateFile(filename, entries); err != nil {
		return fmt.Errorf("failed to write to file. error: %v", err)
	}

	return nil
}

func DetectEntriesByIOControllers(nvmeCtrlPath string, discoveryServicePort uint) ([]*commonstructs.Entry, error) {
	log := logrus.WithFields(logrus.Fields{})
	devices, err := filepath.Glob(nvmeCtrlPath)
	if err != nil {
		return nil, err
	}
	allEntries := []*commonstructs.Entry{}
	for _, d := range devices {
		subsysNqn, err := valueFromFile(filepath.Join(d, "subsysnqn"))
		if err != nil {
			continue
		}
		if !strings.Contains(subsysNqn, "com.lightbitslabs") {
			continue
		}

		transport, err := valueFromFile(filepath.Join(d, "transport"))
		if err != nil {
			continue
		}
		if transport != "tcp" {
			log.Warnf("transport is not of type tcp: %q", transport)
			continue
		}

		hostNqn, err := valueFromFile(filepath.Join(d, "hostnqn"))
		if err != nil {
			log.WithField("error", err).Warnf("failed to read hostnqn")
			continue
		}
		// format: traddr=10.20.58.40,trsvcid=4420
		address, err := valueFromFile(filepath.Join(d, "address"))
		if err != nil {
			log.WithField("error", err).Warn("failed to read address")
			continue
		}
		traddr, _, err := parseAddress(address)
		if err != nil {
			log.WithField("error", err).Warnf("failed to parse address")
			continue
		}
		endpoint := fmt.Sprintf("%s:%d", traddr, discoveryServicePort)
		entries, err := CreateEntries([]string{endpoint}, hostNqn, subsysNqn, transport)
		if err != nil {
			log.WithField("error", err).Warnf("failed to create entries")
			continue
		}
		allEntries = append(allEntries, entries...)
	}
	return allEntries, nil
}

func parseAddress(address string) (string, string, error) {
	params := regexutil.GetParams(addressRegex, address)
	traddr, ok := params["traddr"]
	if !ok {
		return "", "", fmt.Errorf("failed extracting traddr from address: %q", address)
	}
	trsvcid, ok := params["trsvcid"]
	if !ok {
		return "", "", fmt.Errorf("failed extracting trsvcid from address: %q", address)
	}
	return traddr, trsvcid, nil
}

func valueFromFile(filename string) (string, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	value := string(b)
	return strings.TrimSpace(value), nil
}
