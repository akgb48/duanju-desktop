package register

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	hgcrypto "duanju-desktop/pkg/crypto"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrepareWireContract(t *testing.T) {
	now := time.Unix(1700000000, 123000000)
	p, err := NewProfile(now)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Profile: p}
	r, err := c.Prepare(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.OpenUDID) != 16 || p.CDID == "" || p.AID != 8662 || p.DisplayName != "红果短剧" {
		t.Fatalf("bad local profile: %+v", p)
	}
	q := p.BuildQuery(now)
	if len(q) != 40 || q.Get("ts") != "1700000000" || q.Get("_rticket") != "1700000000123" || q.Has("device_id") {
		t.Fatalf("bad registration query: %v", q)
	}
	decoded, err := hgcrypto.TTDecryptV5(r.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, r.Container) {
		t.Fatal("TT payload differs from container")
	}
	var container map[string]json.RawMessage
	if err = json.Unmarshal(decoded, &container); err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err = json.Unmarshal(container["header"], &header); err != nil {
		t.Fatal(err)
	}
	if len(header) != 47 || string(container["magic_tag"]) != `"ss_app_log"` || string(container["_gen_time"]) != "1700000000123" {
		t.Fatalf("bad ss_app_log container: %s", decoded)
	}
	if _, ok := header["device_id"]; ok {
		t.Fatal("invented server ID in local fingerprint")
	}
	sum := md5.Sum(r.Payload)
	if r.Headers.Get("X-SS-Stub") != strings.ToUpper(hex.EncodeToString(sum[:])) || r.Headers.Get("X-Argus") != "APFTZQ==" || r.Headers.Get("X-Khronos") != "1700000000" {
		t.Fatalf("incorrect digest/timestamp headers: %v", r.Headers)
	}
	if r.Headers.Get("X-Medusa") != "" || r.Headers.Get("X-Helios") != "" {
		t.Fatal("registration acquired unverified full-mode headers")
	}
}

func TestRegisterUsesSingleTTEnvelopeAndKeepsLongIDs(t *testing.T) {
	p, err := NewProfile(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/service/2/device_register/" || len(r.URL.Query()) != 40 {
			t.Errorf("incorrect request %s %s", r.Method, r.URL)
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		if !bytes.HasPrefix(body, []byte{'t', 'c', 5, 0x10, 0, 0}) {
			t.Error("TT envelope missing or gzipped twice")
		}
		if _, e = hgcrypto.TTDecryptV5(body); e != nil {
			t.Error(e)
		}
		if r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Content-Type") != "application/octet-stream;tt-data=a" {
			t.Error("incorrect fixed wire headers")
		}
		w.Header().Set("Content-Encoding", "gzip")
		http.SetCookie(w, &http.Cookie{Name: "odin_tt", Value: "opaque-server-value"})
		z := gzip.NewWriter(w)
		io.WriteString(z, `{"device_id":7654321098765432101,"install_id_str":"7654321098765432102","device_token":"opaque"}`)
		z.Close()
	}))
	defer server.Close()
	res, err := (&Client{Profile: p, BaseURL: server.URL, HTTPClient: server.Client()}).Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Identity.DeviceID != "7654321098765432101" || res.Identity.InstallID != "7654321098765432102" || len(res.Cookies) != 1 {
		t.Fatalf("lost server identity: %+v", res)
	}
}

func TestRegisterRejectsEmptyAndZeroIdentity(t *testing.T) {
	p, err := NewProfile(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"", `{"message":"blocked","device_id":0,"install_id":0}`, `{"device_id":"invented","install_id":"7"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			res, err := (&Client{Profile: p, BaseURL: server.URL}).Register(context.Background())
			if err == nil || res != nil {
				t.Fatal("invalid HTTP 200 accepted as registration")
			}
		})
	}
}
