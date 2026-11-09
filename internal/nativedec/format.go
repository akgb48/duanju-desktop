package nativedec

import (
	"errors"
	"strconv"
	"strings"
)

func ffmpegArgs(url string, at float64, hardware bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "0", "-rw_timeout", "10000000"}
	if hardware {
		args = append(args, "-hwaccel", "d3d11va")
	}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	return append(args,
		"-protocol_whitelist", "http,tcp", "-i", url,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", "scale='trunc(iw*min(1,sqrt(2073600/(iw*ih)))/2)*2':'trunc(ih*min(1,sqrt(2073600/(iw*ih)))/2)*2'",
		"-pix_fmt", "yuv420p", "-fps_mode", "cfr",
		"-f", "yuv4mpegpipe", "pipe:1",
	)
}

func parseY4MHeader(line string) (w, h int, fps float64, err error) {
	if !strings.HasPrefix(line, "YUV4MPEG2 ") {
		return 0, 0, 0, errors.New("解码器未输出 Y4M 数据")
	}
	fps = 30
	for _, field := range strings.Fields(line) {
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'W':
			w, _ = strconv.Atoi(field[1:])
		case 'H':
			h, _ = strconv.Atoi(field[1:])
		case 'F':
			parts := strings.Split(field[1:], ":")
			if len(parts) == 2 {
				a, _ := strconv.ParseFloat(parts[0], 64)
				b, _ := strconv.ParseFloat(parts[1], 64)
				if a > 0 && b > 0 {
					fps = a / b
				}
			}
		case 'C':
			if !strings.HasPrefix(field, "C420") {
				return 0, 0, 0, errors.New("不支持的原生像素格式")
			}
		}
	}
	if w < 2 || h < 2 || w%2 != 0 || h%2 != 0 || w > 8192 || h > 8192 || w*h*3/2 > MaxFrameBytes || fps < 1 || fps > 240 {
		return 0, 0, 0, errors.New("原生视频尺寸或帧率超限")
	}
	return
}
