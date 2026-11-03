package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// FixFirewall 添加 Windows 防火墙放行规则。
func (a *App) FixFirewall() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("仅 Windows 支持")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=hgplayer", "dir=in", "action=allow", "program="+exe)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("添加规则失败: %w", err)
	}
	return string(out), nil
}

// FixLoopbackProxy 修复回环代理排除项。
func (a *App) FixLoopbackProxy() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("仅 Windows 支持")
	}
	cmd := exec.Command("netsh", "winhttp", "set", "proxy", "bypass-list", "<local>;127.0.0.1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("设置失败: %w", err)
	}
	return string(out), nil
}

// OpenFirewallSettings 打开系统防火墙设置页。
func (a *App) OpenFirewallSettings() error {
	if runtime.GOOS != "windows" {
		return errors.New("仅 Windows 支持")
	}
	return exec.Command("control.exe", "/name", "Microsoft.WindowsFirewall").Start()
}
