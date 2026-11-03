package app

import (
	"errors"
	"net/http"
	"testing"

	"duanju-desktop/pkg/model"
)

func TestSocialPublishingWithoutAccountMakesNoRequest(t *testing.T) {
	requests := 0
	a := accountTestApp(t, func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected publishing request")
	})
	for name, send := range map[string]func() (*model.Comment, error){
		"comment": func() (*model.Comment, error) { return a.SendComment("123", "456", "测试", "", "[]") },
		"danmaku": func() (*model.Comment, error) { return a.SendDanmaku("123", "456", "测试", 1200, "") },
		"reply": func() (*model.Comment, error) {
			return a.SendReply("123", "456", "789", "", "", "测试", false, 0, "[]")
		},
		"review":       func() (*model.Comment, error) { return a.SendReview("123", "测试", 10) },
		"review reply": func() (*model.Comment, error) { return a.SendReviewReply("123", "789", "", "测试") },
	} {
		t.Run(name, func(t *testing.T) {
			if item, err := send(); err == nil || item != nil {
				t.Fatal("anonymous publishing must fail before network access")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("anonymous publishing made %d network requests", requests)
	}
}
