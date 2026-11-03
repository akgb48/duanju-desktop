// Unauthenticated QR smoke check. Never prints tokens, QR contents, or cookies.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"duanju-desktop/pkg/auth"
	"image/png"
	"os"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	q := auth.NewQR()
	c, err := q.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, _ := base64.StdEncoding.DecodeString(c.Image)
	cfg, err := png.DecodeConfig(strings.NewReader(string(b)))
	if err != nil {
		panic(err)
	}
	p, err := q.Poll(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"imageValid": true, "width": cfg.Width, "height": cfg.Height, "expiresInSec": c.ExpireAt - time.Now().Unix(), "state": p.State, "authenticated": p.Cookies != ""})
}
