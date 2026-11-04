package app

import (
	"duanju-desktop/pkg/register"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DeviceRegistrationState struct {
	Ready       bool   `json:"ready"`
	Registering bool   `json:"registering"`
	Error       string `json:"error"`
}
type deviceRecord struct {
	Profile  *register.Profile `json:"profile"`
	Identity register.Identity `json:"identity"`
	Cookies  string            `json:"cookies,omitempty"`
}

func validDeviceIdentity(id register.Identity) bool {
	for _, s := range []string{id.DeviceID, id.InstallID} {
		if s == "" || strings.Trim(s, "0") == "" {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
func (a *App) readDeviceRecord() (*deviceRecord, error) {
	a.mu.RLock()
	dir := a.dataDir
	a.mu.RUnlock()
	b, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil {
		return nil, err
	}
	var r deviceRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Profile == nil || r.Profile.AID != 8662 || r.Profile.CDID == "" || r.Profile.OpenUDID == "" {
		return nil, errors.New("设备指纹文件无效")
	}
	return &r, nil
}
func (a *App) saveDeviceRecord(r *deviceRecord) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.dataDir, ".device-*.json")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	_ = f.Chmod(0o600)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(a.dataDir, "device.json"))
}
func (a *App) applyDeviceRecord(r *deviceRecord) {
	register.ApplySession(a.sess, r.Profile, r.Identity, r.Cookies)
	a.mu.Lock()
	a.deviceState.Ready = validDeviceIdentity(r.Identity)
	a.mu.Unlock()
}
func (a *App) loadDeviceProfile() error {
	r, err := a.readDeviceRecord()
	if errors.Is(err, os.ErrNotExist) {
		p, e := register.NewProfile(time.Now())
		if e != nil {
			return e
		}
		r = &deviceRecord{Profile: p}
		if err = a.saveDeviceRecord(r); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("读取设备资料失败: %w", err)
	}
	a.applyDeviceRecord(r)
	return nil
}
func (a *App) ensureDeviceRegistration(force bool) (err error) {
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	a.mu.RLock()
	ready := a.deviceState.Ready
	a.mu.RUnlock()
	if ready && !force {
		return nil
	}
	a.mu.Lock()
	a.deviceState.Registering = true
	a.deviceState.Error = ""
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.deviceState.Registering = false
		if err != nil {
			a.deviceState.Error = err.Error()
		}
		a.mu.Unlock()
	}()
	r, err := a.readDeviceRecord()
	if err != nil {
		return fmt.Errorf("读取设备资料失败: %w", err)
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	client := &register.Client{Profile: r.Profile}
	res, err := client.Register(ctx)
	if err != nil {
		return err
	}
	r.Identity = res.Identity
	var cookie []string
	for _, c := range res.Cookies {
		if c.MaxAge >= 0 {
			cookie = append(cookie, (&http.Cookie{Name: c.Name, Value: c.Value}).String())
		}
	}
	for k, v := range map[string]string{"odin_tt": r.Identity.OdinTT, "store-region": r.Identity.StoreRegion, "ttreq": r.Identity.TTReq} {
		if v != "" {
			cookie = append(cookie, (&http.Cookie{Name: k, Value: v}).String())
		}
	}
	r.Cookies = strings.Join(cookie, "; ")
	if err = a.saveDeviceRecord(r); err != nil {
		return fmt.Errorf("保存设备注册结果失败: %w", err)
	}
	a.applyDeviceRecord(r)
	return nil
}
