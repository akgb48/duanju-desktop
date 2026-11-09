//go:build !windows

package nativedec

import (
	"context"
	"errors"
	"os/exec"
)

func attachWebview(ctx context.Context) (sharedView, error) {
	return nil, errors.New("原生共享内存解码仅支持 Windows")
}
func hideProcess(cmd *exec.Cmd)         {}
func guardProcess(cmd *exec.Cmd) func() { return func() {} }
