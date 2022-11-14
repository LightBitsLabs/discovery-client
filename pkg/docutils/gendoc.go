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

package docutils

import (
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var singleFile bool

func NewGenDocCmd(applicationName string) *cobra.Command {
	short := fmt.Sprintf("Generate a Markdown format file for each command in `%s` CLI.", applicationName)
	long := fmt.Sprintf("Generate Markdown documentation for the `%s` CLI.", applicationName)

	cmd := &cobra.Command{
		Use:               "doc",
		Short:             short,
		DisableAutoGenTag: true,
		Long:              long,
		RunE:              gendocCmdFunc,
	}

	cmd.Flags().String("dir", fmt.Sprintf("/tmp/%s-doc/", applicationName), "The directory to write the doc.")

	cmd.Flags().BoolVar(&singleFile, "single-file", false, "generate all commands in single Markdown file.")
	// For bash-completion
	cmd.Flags().SetAnnotation("dir", cobra.BashCompSubdirsInDir, []string{})
	return cmd
}

func gendocCmdFunc(cmd *cobra.Command, args []string) error {
	f := cmd.Flags().Lookup("dir")
	if f == nil {
		log.Fatalf("Flag accessed but not defined for command %s: %s", cmd.Name(), "dir")
	}
	gendocdir := f.Value.String()
	if !strings.HasSuffix(gendocdir, string(os.PathSeparator)) {
		gendocdir += string(os.PathSeparator)
	}
	if _, err := os.Stat(gendocdir); os.IsNotExist(err) {
		log.Println("Directory", gendocdir, "does not exist, creating...")
		os.MkdirAll(gendocdir, 0777)
	}
	prepender := func(filename string) string {
		return ""
	}
	log.Println("Generating LightBox Management command-line documentation in", gendocdir, "...")
	GenMarkdownTreeCustom(cmd.Root(), gendocdir, prepender, singleFile)
	return nil
}
