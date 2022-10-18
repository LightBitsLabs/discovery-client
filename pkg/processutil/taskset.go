package processutil

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func SetupCPUAffinity(cores []int) error {
	if len(cores) == 0 {
		return nil
	}

	runtime.GOMAXPROCS(len(cores))
	pid := os.Getpid()
	var coreList string
	for i, core := range cores {
		coreList += fmt.Sprintf("%d", core)
		if i < len(cores)-1 {
			coreList += ","
		}
	}
	cmd := exec.Command("taskset", "-pca", coreList, fmt.Sprintf("%d", pid))
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
