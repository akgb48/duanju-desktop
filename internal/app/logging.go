package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// dbgPath 是调试日志文件，供实机排查使用。
var dbgPath = filepath.Join(os.TempDir(), "hg-debug.log")

// dbg 追加一行调试日志，失败静默。
//
// 用文件而非 stderr：Wails 在 Windows 上是 GUI 子系统，
// 没有控制台时 stderr 会被丢弃。
func dbg(format string, args ...any) {
	f, err := os.OpenFile(dbgPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}
