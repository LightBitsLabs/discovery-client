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
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sirupsen/logrus"

	"github.com/lightbitslabs/discovery-client/pkg/nvme"
)

const (
	NVMF_DEF_DISC_TMO = 30
	InternalJson      = "internal.json"
)

type Entry struct {
	Transport   string
	Trsvcid     int
	Traddr      string
	Hostnqn     string
	Subsysnqn   string
	Persistent  bool
	Hostaddr    string
	CtrlLossTMO int // time in seconds
}

func (e *Entry) compare(other *Entry) bool {
	if other == nil {
		return false
	}
	if e.Traddr == other.Traddr &&
		e.Persistent == other.Persistent &&
		e.Hostnqn == other.Hostnqn &&
		e.Trsvcid == other.Trsvcid &&
		e.Transport == other.Transport &&
		e.Subsysnqn == other.Subsysnqn &&
		e.CtrlLossTMO == other.CtrlLossTMO {
		return true
	}
	return false
}

func (e *Entry) verify() error {
	if len(e.Subsysnqn) == 0 {
		return fmt.Errorf("Subsysnqn is mandatory")
	}
	if len(e.Traddr) == 0 {
		return fmt.Errorf("Traddr is mandatory")
	}
	if e.Trsvcid == 0 {
		return fmt.Errorf("Trsvcid is mandatory")
	}
	if len(e.Transport) == 0 {
		return fmt.Errorf("Transport is mandatory")
	}
	if len(e.Hostnqn) == 0 {
		return fmt.Errorf("Hostnqn is mandatory")
	}
	if e.CtrlLossTMO < -1 {
		return fmt.Errorf("CtrlLossTMO must be >= -1")
	}
	return nil
}

func EntriesToString(entries []*Entry) string {
	var sb strings.Builder
	for _, entry := range entries {
		sb.WriteString(fmt.Sprintf("%+v\n", entry))
	}
	return sb.String()
}

func trimStringFromHashtag(s string) string {
	if idx := strings.Index(s, "#"); idx != -1 {
		return s[:idx]
	}
	return s
}

func parse(filename string) ([]*Entry, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	splitSpacesAndEqualSign := func(c rune) bool {
		return unicode.IsSpace(c) || string(c) == "="
	}
	scanner := bufio.NewScanner(file)
	var entries []*Entry
	for scanner.Scan() {
		e := &Entry{}
		line := strings.TrimSpace(scanner.Text())
		// remove comments: '#'
		line = trimStringFromHashtag(line)
		// skip empty lines
		if line == "" {
			continue
		}

		s := strings.FieldsFunc(line, splitSpacesAndEqualSign)
		for i := 0; i < len(s); i++ {
			field := strings.TrimSpace(s[i])
			switch field {
			case "-a", "--traddr":
				i++
				value := strings.TrimSpace(s[i])
				_, err = nvme.AdjustTraddr(value)
				if err != nil {
					return nil, &ParserError{
						Msg:     fmt.Sprintf("bad address"),
						Details: fmt.Sprintf("%s is not a valid hostname or IP address", s[i]),
						Err:     err,
					}
				}
				e.Traddr = value
			case "-t", "--transport":
				i++
				value := strings.TrimSpace(s[i])
				if value != "tcp" {
					return nil, &ParserError{
						Msg:     fmt.Sprintf("bad transport"),
						Details: fmt.Sprintf("%s is not a valid transport", s[i]),
						Err:     err,
					}
				}
				e.Transport = value
			case "-s", "--trsvcid":
				i++
				value := strings.TrimSpace(s[i])
				port, err := strconv.ParseInt(value, 10, 32)
				if err != nil {
					return nil, &ParserError{
						Msg:     fmt.Sprintf("bad port"),
						Details: fmt.Sprintf("%s is not a valid int", s[i]),
						Err:     err,
					}
				}
				e.Trsvcid = int(port)
			case "-q", "--hostnqn":
				i++
				e.Hostnqn = strings.TrimSpace(s[i])
			case "-n", "--subsysnqn":
				i++
				e.Subsysnqn = strings.TrimSpace(s[i])
			case "-p", "--persistent":
				e.Persistent = true
			case "-l", "--ctrl-loss-tmo":
				i++
				value := strings.TrimSpace(s[i])
				ctrlLossTMO, err := strconv.ParseInt(value, 10, 32)
				if err != nil {
					return nil, &ParserError{
						Msg:     fmt.Sprintf("bad controller loss timeout value"),
						Details: fmt.Sprintf("%s is not a valid int", s[i]),
						Err:     err,
					}
				}
				e.CtrlLossTMO = int(ctrlLossTMO)
			default:
				return nil, &ParserError{
					Msg:     fmt.Sprintf("unknown flag"),
					Details: fmt.Sprintf("%s is not a vaild flag", field),
					Err:     err,
				}
			}
		}
		if err := e.verify(); err != nil {
			logrus.Warnf("entry: %s not valid. %v", line, err)
			continue
		}
		entries = append(entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return removeDupEntries(entries), nil
}

func removeDupEntries(entries []*Entry) []*Entry {
	// Implementing a kind of set data structure by utilizing the fact that map keys are unique
	entriesSet := map[Entry]bool{}
	for _, e := range entries {
		entriesSet[*e] = true
	}
	// From the set we construct a slice of unique entries PTRs
	uniqueEntries := []*Entry{}
	for e := range entriesSet {
		var entry = e
		uniqueEntries = append(uniqueEntries, &entry)
	}
	return uniqueEntries
}

type referrals struct {
	Entries      []Entry   `json:"entries,omitempty"`
	CreationTime time.Time `json:"creation_time"`
}

func lastUpdate(path string) (updateTime time.Time, err error) {
	stat, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return stat.ModTime(), nil
}
