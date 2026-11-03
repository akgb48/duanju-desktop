// Command appresources packages the application artwork into Windows resources.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func main() {
	root := flag.String("root", "../..", "project root")
	arch := flag.String("arch", "amd64", "Windows architecture")
	versionOverride := flag.String("version", "", "optional release version")
	flag.Parse()
	if err := build(*root, *arch, *versionOverride); err != nil {
		log.Fatal(err)
	}
}

func build(root, architecture, release string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	arches := map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64, "386": winres.ArchI386}
	arch, ok := arches[architecture]
	if !ok {
		return fmt.Errorf("unsupported architecture: %s", architecture)
	}
	input, err := os.Open(filepath.Join(root, "build", "appicon.png"))
	if err != nil {
		return err
	}
	artwork, err := png.Decode(input)
	input.Close()
	if err != nil {
		return err
	}
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	icon, err := winres.NewIconFromResizedImage(artwork, sizes)
	if err != nil {
		return err
	}
	var encoded bytes.Buffer
	if err = icon.SaveICO(&encoded); err != nil {
		return err
	}
	windowsDir := filepath.Join(root, "build", "windows")
	if err = os.MkdirAll(windowsDir, 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(windowsDir, "icon.ico"), encoded.Bytes(), 0o644); err != nil {
		return err
	}
	// ICO frames are PNG payloads. Reuse the 256px frame for both UI builds.
	data := encoded.Bytes()
	var thumbnail []byte
	for index := 0; index < int(binary.LittleEndian.Uint16(data[4:6])); index++ {
		offset := 6 + index*16
		if data[offset] == 0 && data[offset+1] == 0 {
			length := binary.LittleEndian.Uint32(data[offset+8:])
			start := binary.LittleEndian.Uint32(data[offset+12:])
			thumbnail = data[start : start+length]
			break
		}
	}
	if len(thumbnail) == 0 {
		return fmt.Errorf("256px icon frame missing")
	}
	for _, folder := range []string{"frontend/assets", "frontend/public"} {
		if err = os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(root, folder, "app-icon.png"), thumbnail, 0o644); err != nil {
			return err
		}
	}
	configData, err := os.ReadFile(filepath.Join(root, "wails.json"))
	if err != nil {
		return err
	}
	var config struct {
		Info struct {
			ProductName    string `json:"productName"`
			ProductVersion string `json:"productVersion"`
			CompanyName    string `json:"companyName"`
			Copyright      string `json:"copyright"`
		} `json:"info"`
	}
	if err = json.Unmarshal(configData, &config); err != nil {
		return err
	}
	if release == "" {
		release = config.Info.ProductVersion
	}
	info := version.Info{Type: version.App}
	coreVersion := strings.Split(strings.TrimPrefix(release, "v"), "-")[0]
	for index, part := range strings.Split(coreVersion, ".") {
		if index >= 4 {
			return fmt.Errorf("invalid Windows version: %s", release)
		}
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return err
		}
		info.FileVersion[index], info.ProductVersion[index] = uint16(number), uint16(number)
	}
	for key, value := range map[string]string{
		version.ProductName: config.Info.ProductName, version.FileDescription: config.Info.ProductName,
		version.CompanyName: config.Info.CompanyName, version.LegalCopyright: config.Info.Copyright,
		version.FileVersion: release, version.ProductVersion: release,
	} {
		if err = info.Set(version.LangNeutral, key, value); err != nil {
			return err
		}
	}
	resources := winres.ResourceSet{}
	// Wails loads icon group 3 for native windows and the taskbar.
	if err = resources.SetIcon(winres.ID(3), icon); err != nil {
		return err
	}
	resources.SetVersionInfo(info)
	output := filepath.Join(root, "app_resources_windows_"+architecture+".syso")
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	err = resources.WriteObject(file, arch)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("Windows resources ready: icon group 3, %d sizes, version %s\n", len(sizes), release)
	return nil
}
