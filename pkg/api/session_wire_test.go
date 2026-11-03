package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	signing "duanju-desktop/pkg/crypto"
)

type wireSigner struct {
	input  signing.SignInput
	called int
}

func (s *wireSigner) Sign(r *http.Request, in signing.SignInput) {
	s.called++
	s.input = in
	s.input.Body = append([]byte(nil), in.Body...)
	r.Header.Set("X-Test-Signature", "recorded")
}

func decodeWireBody(t *testing.T, r *http.Request) ([]byte, []byte) {
	t.Helper()
	encoded, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
		return nil, nil
	}
	if r.Header.Get("Content-Encoding") != "gzip" {
		return encoded, encoded
	}
	reader, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Error(err)
		return encoded, nil
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Error(err)
	}
	return encoded, decoded
}

func TestPOSTJSONSeparatesPublicQueryAndPreservesBusinessObject(t *testing.T) {
	var wire, logical []byte
	var query url.Values
	var requestSignature string
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		requestSignature = r.Header.Get("X-Test-Signature")
		wire, logical = decodeWireBody(t, r)
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	s.SetParam("aid", "explicit-aid")
	s.SetParam("_rticket", "")
	sg := &wireSigner{}
	s.SetSigner(sg)
	body := struct {
		SeriesID int64               `json:"series_id"`
		Filters  map[string][]string `json:"select_items"`
		Device   string              `json:"device_id"`
	}{9007199254740993123, map[string][]string{"genre": {"short_drama", "悬疑"}}, "business-only"}
	_, err := s.CallJSON(context.Background(), "category_landpage", body)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(logical, expected) {
		t.Fatalf("business JSON changed:\n got %s\nwant %s", logical, expected)
	}
	if query.Get("aid") != "explicit-aid" {
		t.Fatalf("explicit session value overwritten: %v", query)
	}
	if _, ok := query["_rticket"]; !ok || query.Get("_rticket") != "" {
		t.Fatalf("explicit empty default overwritten: %v", query)
	}
	if query.Get("device_id") != s.DeviceContext().DeviceID {
		t.Fatalf("public device query incorrectly mixed: %v", query)
	}
	if query.Has("select_items") || query.Has("series_id") {
		t.Fatalf("business fields leaked to query: %v", query)
	}
	if sg.called != 1 || requestSignature != "recorded" {
		t.Fatal("signer was not applied")
	}
	if !bytes.Equal(sg.input.Body, wire) {
		t.Fatal("signer did not receive actual gzip-transformed wire bytes")
	}
	if sg.input.Query != query.Encode() {
		t.Fatalf("signer query differs: %q vs %q", sg.input.Query, query.Encode())
	}
}

func TestGETQueryPreservesTypedLargeNumbersAndOverrides(t *testing.T) {
	var got url.Values
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		b, _ := io.ReadAll(r.Body)
		if len(b) != 0 {
			t.Error("GET body must be empty")
		}
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	_, err := s.CallJSON(context.Background(), "search", struct {
		ID  int64  `json:"book_id"`
		AID string `json:"aid"`
	}{9007199254740993123, "caller-aid"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("book_id") != "9007199254740993123" || got.Get("aid") != "caller-aid" {
		t.Fatalf("typed query precision/precedence failed: %v", got)
	}
}

func TestInvalidJSONBodyFailsBeforeNetwork(t *testing.T) {
	called := false
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { called = true; io.WriteString(w, `{}`) })
	for _, action := range []string{"comment_add", "search"} {
		if _, err := s.CallJSON(context.Background(), action, map[string]any{"invalid": make(chan int)}); err == nil {
			t.Fatalf("%s accepted unserializable input", action)
		}
	}
	if called {
		t.Fatal("invalid JSON reached HTTP transport")
	}
}

func TestFormBodyUsesBusinessFieldsAndPublicQuery(t *testing.T) {
	var query, form url.Values
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "" {
			t.Error("form must not be gzipped")
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("incorrect form content type")
		}
		query = r.URL.Query()
		body, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(body))
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	_, err := s.CallBody(context.Background(), "sms_login", map[string]any{"mobile": "encoded-number", "code": "encoded-code"})
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("mobile") != "encoded-number" || form.Get("code") != "encoded-code" || form.Has("device_id") {
		t.Fatalf("form fields mixed: %v", form)
	}
	if query.Get("device_id") != s.DeviceContext().DeviceID || query.Has("mobile") {
		t.Fatalf("form query mixed: %v", query)
	}
}

func TestEncodedBodyHasExactBytesAndExplicitQuery(t *testing.T) {
	var got []byte
	var query url.Values
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		query = r.URL.Query()
		if r.Header.Get("Content-Encoding") != "" {
			t.Error("encoded protocol body was compressed again")
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("content type changed")
		}
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	s.SetParam("must_not_merge", "x")
	payload := []byte{0, 1, 2, 3, 0xff, 0xaa}
	q := url.Values{"exact": {"one", "two"}, "device_id": {"fixture-device"}}
	_, err := s.CallRawWithQuery(context.Background(), "multi_video_model", "application/octet-stream", q, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, got) {
		t.Fatalf("encoded bytes changed: %x", got)
	}
	if query.Encode() != q.Encode() || query.Has("must_not_merge") {
		t.Fatalf("query defaults added: %v", query)
	}
}

func TestResponseCookieReplacementAppliesToNextRequestEvenOnDecodeError(t *testing.T) {
	call := 0
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "new==", Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: "new-name", Value: "added"})
			w.WriteHeader(503)
			io.WriteString(w, "temporarily unavailable")
			return
		}
		if v, err := r.Cookie("token"); err != nil || v.Value != "new==" {
			t.Errorf("same-name cookie not updated: %v %v", v, err)
		}
		if v, err := r.Cookie("keep"); err != nil || v.Value != "untouched" {
			t.Errorf("unrelated cookie lost: %v %v", v, err)
		}
		if v, err := r.Cookie("new-name"); err != nil || v.Value != "added" {
			t.Errorf("new cookie lost: %v %v", v, err)
		}
		if strings.Contains(r.Header.Get("Cookie"), "Path=") || strings.Contains(r.Header.Get("Cookie"), "HttpOnly") {
			t.Error("Set-Cookie attributes leaked into Cookie")
		}
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	s.SetCookieString("keep=untouched; token=old")
	if _, err := s.Call(context.Background(), "user_info", nil); err == nil {
		t.Fatal("invalid response should fail")
	}
	if _, err := s.Call(context.Background(), "user_info", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(s.CookieString(), "token=") != 1 {
		t.Fatal("duplicate same-name cookies")
	}
}

func TestBaseRouteDoesNotUseBusinessSigner(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"code":0,"data":{}}`) })
	s.registry = NewRegistry()
	s.registry.MustRegister(&API{Action: "base-test", Path: "/base", Method: GET, Route: RouteBase})
	sg := &wireSigner{}
	s.SetSigner(sg)
	if _, err := s.Call(context.Background(), "base-test", nil); err != nil {
		t.Fatal(err)
	}
	if sg.called != 0 {
		t.Fatal("base service received business signature")
	}
}
