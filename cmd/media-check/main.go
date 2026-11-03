// Local inspection of the previously captured encrypted media header.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"duanju-desktop/pkg/media/cenc"
	"os"
)

func main() {
	b, err := os.ReadFile(".runtime-signed-probe/multi_video_model-wire.json")
	if err != nil {
		panic(err)
	}
	var r struct {
		Data map[string]struct {
			VideoModel string `json:"video_model"`
		}
	}
	if err = json.Unmarshal(b, &r); err != nil {
		panic(err)
	}
	var m struct {
		VideoList []struct {
			EncryptInfo struct {
				SpadeA string `json:"spade_a"`
			} `json:"encrypt_info"`
			VideoMeta struct {
				Size int64 `json:"size"`
			} `json:"video_meta"`
		} `json:"video_list"`
	}
	for _, v := range r.Data {
		if err = json.Unmarshal([]byte(v.VideoModel), &m); err != nil {
			panic(err)
		}
		break
	}
	key, err := cenc.DeriveContentKey(m.VideoList[0].EncryptInfo.SpadeA)
	if err != nil {
		panic(err)
	}
	header, err := os.ReadFile(".runtime-signed-probe/encrypted-header.mp4")
	if err != nil {
		panic(err)
	}
	layout, err := cenc.ReadMP4Layout(bytes.NewReader(header), m.VideoList[0].VideoMeta.Size, key)
	if err != nil {
		panic(err)
	}
	if err = layout.Transform(header, 0); err != nil {
		panic(err)
	}
	if err = os.WriteFile(".runtime-signed-probe/clear-header.mp4", header, 0600); err != nil {
		panic(err)
	}
	fmt.Printf("CENC: %d encrypted sample regions, %d bytes transformed, unchanged total %d bytes\n", layout.SampleRegions(), len(header), layout.Size)
}
