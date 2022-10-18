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
