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
