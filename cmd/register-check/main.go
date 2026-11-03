// register-check sends one registration request and reports only its outcome.
// It does not print or save identifiers, tokens, cookies, or the fingerprint.
package main

import (
	"context"
	"duanju-desktop/pkg/register"
	"encoding/json"
	"os"
	"time"
)

func main() {
	p, err := register.NewProfile(time.Now())
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		_, err = (&register.Client{Profile: p}).Register(ctx)
	}
	out := map[string]any{"registered": err == nil}
	if err != nil {
		out["error"] = err.Error()
	}
	json.NewEncoder(os.Stdout).Encode(out)
	if err != nil {
		os.Exit(1)
	}
}
