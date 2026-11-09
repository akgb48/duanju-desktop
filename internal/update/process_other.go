//go:build !windows

package update

import (
	"errors"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd)    {}
func waitForParent(pid int) error { return errors.New("仅 Windows 支持原地更新") }
