package app

import (
	"duanju-desktop/pkg/model"
	"errors"
	"fmt"
)

// ResolveShare 解析分享链接或口令。
func (a *App) ResolveShare(text string) (*model.Series, error) {
	if text == "" {
		return nil, errors.New("请粘贴分享链接或口令")
	}
	id := extractSeriesID(text)
	if id == "" {
		return nil, errors.New("无法从内容中识别剧集 ID")
	}
	return a.seriesDetail(id)
}

// ShareLink 生成某剧的分享链接。kind 1=短链 2=口令。
func (a *App) ShareLink(seriesID string, kind int) (string, error) {
	if seriesID == "" {
		return "", errors.New("缺少剧集 ID")
	}
	switch kind {
	case 2:
		return fmt.Sprintf("【短剧电脑版】发现一部好剧 %s", seriesID), nil
	default:
		return fmt.Sprintf("https://hg.example/share/%s", seriesID), nil
	}
}

// extractSeriesID 从文本里抠出剧集 ID（纯数字段）。
func extractSeriesID(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			continue
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j-i >= 6 {
			return s[i:j]
		}
		i = j
	}
	return ""
}
