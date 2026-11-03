package app

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 本文件是前端绑定契约的守卫测试。
//
// 它做三件事：
//  1. App 上恰好有 bindingNames 列出的一批导出方法，且没有多余导出
//  2. 每个方法的参数个数与 frontend/src/api/bridge.js 调用点一致
//  3. 方法数等于 BindingCount

// exportedMethods 返回 *App 上所有导出方法名（排序后）。
func exportedMethods(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(&App{})
	var out []string
	for i := 0; i < typ.NumMethod(); i++ {
		out = append(out, typ.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

// TestBindingCount 校验绑定数量。
func TestBindingCount(t *testing.T) {
	if len(bindingNames) != BindingCount {
		t.Fatalf("bindingNames 有 %d 条，BindingCount 声明 %d 条", len(bindingNames), BindingCount)
	}
}

// TestBindingNamesUnique 校验没有重名。
func TestBindingNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range bindingNames {
		if seen[n] {
			t.Errorf("重复的绑定名: %s", n)
		}
		seen[n] = true
	}
}

// TestAllBindingsImplemented 校验每个声明的方法都在 App 上存在。
func TestAllBindingsImplemented(t *testing.T) {
	have := map[string]bool{}
	for _, m := range exportedMethods(t) {
		have[m] = true
	}
	var missing []string
	for _, n := range bindingNames {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("App 上缺少 %d 个方法：\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestNoExtraBindings 确认没有声明外的导出方法（Wails 会把它们也暴露给前端）。
func TestNoExtraBindings(t *testing.T) {
	declared := map[string]bool{}
	for _, n := range bindingNames {
		declared[n] = true
	}
	// 生命周期与内部方法不属于前端 API。
	exempt := map[string]bool{
		"OnStartup": true, "OnShutdown": true,
	}
	var extra []string
	for _, m := range exportedMethods(t) {
		if declared[m] || exempt[m] {
			continue
		}
		extra = append(extra, m)
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("有 %d 个未声明的导出方法（会意外暴露给前端）：\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}

func TestBridgeArityMatches(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	if !pathExists(root) {
		t.Skip("前端源码不存在，跳过")
	}

	arity := scanFrontendArity(t, root)
	typ := reflect.TypeOf(&App{})

	for name, counts := range arity {
		m, ok := typ.MethodByName(name)
		if !ok {
			t.Errorf("前端调用了 %s，但 App 上没有这个方法", name)
			continue
		}
		// 方法签名是 (receiver, args...)；可变参数算作 1。
		want := m.Type.NumIn() - 1
		for argc := range counts {
			if argc != want {
				t.Errorf("%s: 前端传 %d 个参数，Go 侧接受 %d 个", name, argc, want)
			}
		}
	}
}

// scanFrontendArity 扫描 .vue/.js 里 api.X(...) 的实参个数。
func scanFrontendArity(t *testing.T, root string) map[string]map[int]bool {
	t.Helper()
	out := map[string]map[int]bool{}
	re := regexp.MustCompile(`\bapi\.(\w+)\s*\(`)

	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".vue") && !strings.HasSuffix(p, ".js") {
			return nil
		}
		if strings.HasSuffix(p, "bridge.js") {
			return nil
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		src := string(raw)
		for _, loc := range re.FindAllStringSubmatchIndex(src, -1) {
			name := src[loc[2]:loc[3]]
			open := loc[1] - 1 // '(' 的位置
			depth := 0
			i := open
			for ; i < len(src); i++ {
				switch src[i] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					break
				}
			}
			inner := strings.TrimSpace(src[open+1 : i])
			n := 0
			if inner != "" {
				n = 1
				d := 0
				for _, c := range inner {
					switch c {
					case '(', '[', '{':
						d++
					case ')', ']', '}':
						d--
					case ',':
						if d == 0 {
							n++
						}
					}
				}
				if strings.HasSuffix(inner, ",") {
					n--
				}
			}
			if out[name] == nil {
				out[name] = map[int]bool{}
			}
			out[name][n] = true
		}
		return nil
	})
	return out
}

// TestBridgeDeclaresAllBindings 校验 bridge.js 声明的 call() 名与 bindingNames 一致。
func TestBridgeDeclaresAllBindings(t *testing.T) {
	p := filepath.Join("..", "..", "frontend", "src", "api", "bridge.js")
	if !pathExists(p) {
		t.Skip("bridge.js 不存在，跳过")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 bridge.js 失败: %v", err)
	}
	re := regexp.MustCompile(`export const (\w+) = call\('(\w+)'\)`)
	jsNames := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		jsNames[m[2]] = true
	}

	goNames := map[string]bool{}
	for _, n := range bindingNames {
		goNames[n] = true
	}

	var onlyJS, onlyGo []string
	for n := range jsNames {
		if !goNames[n] {
			onlyJS = append(onlyJS, n)
		}
	}
	for n := range goNames {
		if !jsNames[n] {
			onlyGo = append(onlyGo, n)
		}
	}
	sort.Strings(onlyJS)
	sort.Strings(onlyGo)
	if len(onlyJS) > 0 {
		t.Errorf("bridge.js 声明但 Go 未实现（%d）：%v", len(onlyJS), onlyJS)
	}
	if len(onlyGo) > 0 {
		t.Errorf("Go 声明但 bridge.js 未包装（%d）：%v", len(onlyGo), onlyGo)
	}
}
