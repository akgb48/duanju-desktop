package crypto

import "testing"

// 固定输入与签名结果的回归向量。
func TestGorgonWithNonceVectors(t *testing.T) {
	cases := []struct {
		query, body string
		ts          uint32
		nonce       uint16
		want        string
	}{
		{"", "", 0, 0, "8404000040011d7e264c4ddc29903209e4a62085e1b2c51a6694"},
		{"aid=8662&x=%E4%B8%AD", "", 1700000000, 0x1234, "8404341240012fcf4af39676b409faaf9a63085f75ace5d1f78c"},
		{"a=1", `{"x":1}`, 0xffffffff, 0xffff, "8404ffff4001cea6b4b9e054fbab08fb85d22a58a886cbfd7bb6"},
	}
	for _, c := range cases {
		if got := GorgonWithNonce(c.query, []byte(c.body), c.ts, c.nonce); got != c.want {
			t.Errorf("ts=%d nonce=%x got %s want %s", c.ts, c.nonce, got, c.want)
		}
	}
}
