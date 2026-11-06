package api

import (
	"fmt"
	"sort"
	"sync"
)

// Method 是 HTTP 方法名。
type Method string

const (
	GET  Method = "GET"
	POST Method = "POST"
)

// API 描述一条上游接口的静态元信息。
type API struct {
	// Action 是调用方使用的逻辑名，全局唯一。
	Action string
	// Path 是 URL 路径（不含主机）。
	Path string
	// Method 是 HTTP 方法。
	Method Method
	// Host 固定主机；为空表示走默认主机池轮询。
	Host string
	// Route 是路由类别，决定该请求落到哪套后端。
	Route Route
	// Doc 是给人看的说明。
	Doc string

	playerWire bool
}

// Route 标识后端服务族。
type Route uint8

const (
	// RouteContent 走 *.fqnovel.com 内容 API，需完整风控签名。
	RouteContent Route = iota + 1
	// RoutePassport 走 *.fqnovel.com 的账号子集，签名规则同内容 API。
	RoutePassport
	// RouteBase 走 *.snssdk.com 基础服务，不带业务签名。
	RouteBase
	// RouteUpdate 走自建更新服务器，无风控签名，响应侧 ed25519 验签。
	RouteUpdate
)

func (r Route) String() string {
	switch r {
	case RouteContent:
		return "content"
	case RoutePassport:
		return "passport"
	case RouteBase:
		return "base"
	case RouteUpdate:
		return "update"
	}
	return "unknown"
}

// Registry 保存全部已注册接口。
//
// 零值不可用，必须经 NewRegistry 构造。
type Registry struct {
	mu       sync.RWMutex
	byAction map[string]*API
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{byAction: make(map[string]*API, 64)}
}

// Register 注册一条接口。action 重复或字段非法时返回错误。
func (r *Registry) Register(a *API) error {
	if a == nil {
		return fmt.Errorf("api: 注册 nil 接口")
	}
	if a.Action == "" {
		return fmt.Errorf("api: 接口缺少 action")
	}
	if a.Path == "" || a.Path[0] != '/' {
		return fmt.Errorf("api[%s]: path 必须以 / 开头", a.Action)
	}
	if a.Method != GET && a.Method != POST {
		return fmt.Errorf("api[%s]: 非法 method %q", a.Action, a.Method)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byAction[a.Action]; dup {
		return fmt.Errorf("api[%s]: 重复注册", a.Action)
	}
	r.byAction[a.Action] = a
	return nil
}

// MustRegister 注册失败时 panic，仅用于包级初始化。
func (r *Registry) MustRegister(a *API) {
	if err := r.Register(a); err != nil {
		panic(err)
	}
}

// Get 按 action 取接口定义。
func (r *Registry) Get(action string) (*API, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byAction[action]
	return a, ok
}

// Names 返回全部 action，按字典序排列。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byAction))
	for k := range r.byAction {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// All 返回全部接口定义，按 action 字典序排列。
func (r *Registry) All() []*API {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*API, 0, len(r.byAction))
	for _, a := range r.byAction {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

// Len 返回已注册接口数。
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byAction)
}
