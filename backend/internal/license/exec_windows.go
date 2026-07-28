//go:build windows

package license

import (
	"os/exec"
	"strings"
)

func init() {
	SetExecCommand(func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		data, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	})
}