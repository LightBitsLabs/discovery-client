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

package commonstructs

import (
	"fmt"
	"strings"
)

type Entry struct {
	Transport string
	Traddr    string
	Trsvcid   int
	Hostnqn   string
	Nqn       string
}

func (entry *Entry) String() string {
	return fmt.Sprintf("-t %s -a %s -s %d -q %s -n %s\n", entry.Transport, entry.Traddr, entry.Trsvcid, entry.Hostnqn, entry.Nqn)
}

func EntriesToString(entries []*Entry) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(fmt.Sprintf("%s\n", entry))
	}
	return b.String()
}
