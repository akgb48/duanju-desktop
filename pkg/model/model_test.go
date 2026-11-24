package model

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// intentionallyEmpty 记录有意保持零值的设置字段。
//
// DefaultSettings 新增字段却忘了给默认值时，TestDefaultSettingsSetsEveryField
// 会失败，提醒作者要么补上默认值，要么把字段名加进这张表并说明原因。
var intentionallyEmpty = map[string]struct{}{
	"DownloadDir": {}, // 留空，由运行期按用户目录推导
	"ProxyURL":    {}, // 留空表示直连
	"BossKey":     {}, // 留空表示未绑定老板键
}

// TestDefaultSettingsSetsEveryField 防止新增设置项后默认值为零值，
// 导致界面出现空主题、零并发、打不开起始页这类问题。
func TestDefaultSettingsSetsEveryField(t *testing.T) {
	value := reflect.ValueOf(DefaultSettings())
	typ := value.Type()

	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := intentionallyEmpty[name]; ok {
			continue
		}
		if value.Field(i).IsZero() {
			t.Errorf("DefaultSettings 漏了字段 %s，请在默认值中赋值，或加入 intentionallyEmpty 说明为何留空", name)
		}
	}

	// 反向检查：白名单里的字段若已经有了默认值，说明这张表过期了。
	for name := range intentionallyEmpty {
		field := value.FieldByName(name)
		if !field.IsValid() {
			t.Errorf("intentionallyEmpty 中的 %s 已不是 Settings 的字段，请清理", name)
			continue
		}
		if !field.IsZero() {
			t.Errorf("intentionallyEmpty 中的 %s 现在有默认值了，请从表中移除", name)
		}
	}
}

// crossLayerTypes 是同时用于解析上游 JSON、传给前端、写入 SQLite 的类型，
// 它们的 json tag 即是跨层契约。
func crossLayerTypes() []any {
	return []any{
		Series{}, TitleTag{}, Episode{}, PlaySource{}, VideoDetail{},
		FeedItem{}, FeedReason{}, FeedPage{}, Comment{}, CommentImage{},
		RelatedSeries{}, CommentPage{}, User{}, Account{}, HistoryEntry{},
		SearchResult{}, SearchHighlight{}, SearchSuggestion{}, Settings{},
		Region{}, Notice{},
	}
}

// TestJSONTagsArePresentAndUnique 校验每个导出字段都有 json tag，
// 且同一结构体内不出现重名（重名会让上层字段静默覆盖下层字段）。
func TestJSONTagsArePresentAndUnique(t *testing.T) {
	for _, sample := range crossLayerTypes() {
		typ := reflect.TypeOf(sample)
		seen := make(map[string]string, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag, ok := field.Tag.Lookup("json")
			if !ok {
				t.Errorf("%s.%s 缺少 json tag", typ.Name(), field.Name)
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name == "" || name == "-" {
				continue
			}
			if prev, dup := seen[name]; dup {
				t.Errorf("%s 的 json 名 %q 被 %s 与 %s 重复使用", typ.Name(), name, prev, field.Name)
			}
			seen[name] = field.Name
		}
	}
}

// TestSettingsJSONRoundTrip 确保设置项能完整往返，
// 避免新增字段时漏掉 tag 导致设置读回后丢失。
func TestSettingsJSONRoundTrip(t *testing.T) {
	want := DefaultSettings()

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("序列化设置失败: %v", err)
	}

	var got Settings
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("反序列化设置失败: %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("设置往返后不一致：\nwant = %+v\ngot  = %+v", want, got)
	}
}

// TestPlaySourceContentKeyStaysLocal 守住一条安全边界：
// 播放密钥只应留在后端供 MP4 分片变换使用。一旦它被序列化进 JSON，
// 就等于把密钥直接交给了前端、调试日志和任何抓包工具。
//
// 注意 []byte 会被 encoding/json 编码成 Base64，因此不能靠查找明文判断泄露，
// 这里改为「结构断言 + 行为断言」双重校验。
func TestPlaySourceContentKeyStaysLocal(t *testing.T) {
	// 断言一：标签必须显式排除序列化。字段改名或漏写标签都会让密钥按字段名输出。
	field, ok := reflect.TypeOf(PlaySource{}).FieldByName("ContentKey")
	if !ok {
		t.Fatal("PlaySource 缺少 ContentKey 字段")
	}
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		t.Fatal("ContentKey 缺少 json 标签，默认会按字段名序列化")
	}
	if name := strings.Split(tag, ",")[0]; name != "-" {
		t.Fatalf(`ContentKey 的 json 标签必须为 "-"，当前为 %q`, tag)
	}

	// 断言二：设置密钥不应改变序列化结果，字段名与取值都不允许出现在 JSON 中。
	const secret = "super-secret-key-material"

	plain := PlaySource{Quality: "720p", URL: "https://example.com/a.mp4"}
	withKey := plain
	withKey.ContentKey = []byte(secret)

	plainJSON, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("序列化无密钥播放源失败: %v", err)
	}
	keyJSON, err := json.Marshal(withKey)
	if err != nil {
		t.Fatalf("序列化带密钥播放源失败: %v", err)
	}

	if string(plainJSON) != string(keyJSON) {
		t.Fatalf("设置 ContentKey 后 JSON 发生变化，密钥已泄露：\n无密钥 = %s\n有密钥 = %s", plainJSON, keyJSON)
	}

	// 双保险：即使字段名被改，也不允许出现密钥本身或它的 Base64 形式。
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	for _, fragment := range []string{secret, encoded, "content_key"} {
		if strings.Contains(strings.ToLower(string(keyJSON)), strings.ToLower(fragment)) {
			t.Fatalf("JSON 中出现 %q，播放密钥已泄露：%s", fragment, keyJSON)
		}
	}
}
