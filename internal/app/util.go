package app

import (
	"strconv"
	"strings"
)

// 本文件放 internal/app 内部的小工具，避免各 api_*.go 重复实现。

// parseTabType 把前端传来的页签标识解析成数字 tab_type。
//
// 空字符串使用默认页签，数字字符串转换为整数，其他输入交由调用方兜底。
func parseTabType(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}
