package hgapi

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
)

func buildMultipart(field, filename string, data []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", "application/octet-stream")

	part, err := w.CreatePart(h)
	if err != nil {
		return nil, "", fmt.Errorf("hgapi: 构造 multipart 失败: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("hgapi: 写入 multipart 失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("hgapi: 收尾 multipart 失败: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}
