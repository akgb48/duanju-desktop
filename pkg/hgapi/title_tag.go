package hgapi

import (
	"duanju-desktop/pkg/model"
	"regexp"
)

var titleTagColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)

func parseTitleTag(data map[string]any) *model.TitleTag {
	tag := &model.TitleTag{Text: text(data, "text"), Icon: text(data, "icon_url", "icon"), Source: "server"}
	if tag.Text == "" && tag.Icon == "" {
		return nil
	}
	if colors, ok := data["bg_color"].([]any); ok {
		for _, v := range colors {
			if value, ok := v.(string); ok && titleTagColor.MatchString(value) {
				tag.Colors = append(tag.Colors, value)
			}
		}
	}
	if color := text(data, "text_color"); titleTagColor.MatchString(color) {
		tag.TextColor = color
	}
	return tag
}
