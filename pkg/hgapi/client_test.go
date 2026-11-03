package hgapi

import (
	"testing"
)

// TestFlexInt 验证宽容整数解析。
func TestFlexInt(t *testing.T) {
	cases := map[string]int64{
		`123`:   123,
		`"456"`: 456,
		`0`:     0,
		`""`:    0,
		`null`:  0,
		`"abc"`: 0,
		`-7`:    -7,
		`1.9`:   0, // 浮点非整数记 0
	}
	for raw, want := range cases {
		var f flexInt
		if err := f.UnmarshalJSON([]byte(raw)); err != nil {
			t.Errorf("UnmarshalJSON(%s) 报错: %v", raw, err)
			continue
		}
		if int64(f) != want {
			t.Errorf("flexInt(%s) = %d，期望 %d", raw, f, want)
		}
	}
}

// TestFlexFloat 验证宽容小数解析。
func TestFlexFloat(t *testing.T) {
	cases := map[string]float64{
		`9.5`:   9.5,
		`"8.1"`: 8.1,
		`0`:     0,
		`""`:    0,
		`null`:  0,
	}
	for raw, want := range cases {
		var f flexFloat
		_ = f.UnmarshalJSON([]byte(raw))
		if float64(f) != want {
			t.Errorf("flexFloat(%s) = %v，期望 %v", raw, f, want)
		}
	}
}

// TestFlexBool 验证宽容布尔解析。
func TestFlexBool(t *testing.T) {
	cases := map[string]bool{
		`1`:      true,
		`"1"`:    true,
		`true`:   true,
		`"true"`: true,
		`"yes"`:  true,
		`0`:      false,
		`"0"`:    false,
		`false`:  false,
		`null`:   false,
		`""`:     false,
	}
	for raw, want := range cases {
		var f flexBool
		_ = f.UnmarshalJSON([]byte(raw))
		if bool(f) != want {
			t.Errorf("flexBool(%s) = %v，期望 %v", raw, f, want)
		}
	}
}

// TestIDString 验证 ID 归一化。
func TestIDString(t *testing.T) {
	cases := map[string]string{
		`123`:       "123",
		`"456"`:     "456",
		`null`:      "",
		`"0"`:       "0",
		`"7299abc"`: "7299abc",
	}
	for raw, want := range cases {
		if got := idString([]byte(raw)); got != want {
			t.Errorf("idString(%s) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestFmtScore 验证分数格式化。
func TestFmtScore(t *testing.T) {
	cases := map[float64]string{
		9.5:  "9.5",
		8.0:  "8.0",
		0:    "",
		-1:   "",
		7.25: "7.2", // 一位小数，向下取
	}
	for in, want := range cases {
		if got := fmtScore(in); got != want {
			t.Errorf("fmtScore(%v) = %q，期望 %q", in, got, want)
		}
	}
}
