package app

import (
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/register"
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceProfilePersistsWithoutInventingRegisteredIDs(t *testing.T) {
	dir := t.TempDir()
	a := New()
	a.dataDir = dir
	a.sess = api.NewSession()
	if err := a.loadDeviceProfile(); err != nil {
		t.Fatal(err)
	}
	before := a.sess.DeviceContext()
	if before.DeviceID != "" || before.InstallID != "" || a.deviceState.Ready {
		t.Fatal("fresh fingerprint was treated as registered")
	}
	second := New()
	second.dataDir = dir
	second.sess = api.NewSession()
	if err := second.loadDeviceProfile(); err != nil {
		t.Fatal(err)
	}
	after := second.sess.DeviceContext()
	if before.CDID != after.CDID || before.OpenUDID != after.OpenUDID {
		t.Fatal("fingerprint changed on restart")
	}
	r, err := second.readDeviceRecord()
	if err != nil {
		t.Fatal(err)
	}
	r.Identity = register.Identity{DeviceID: "7654321098765432101", InstallID: "7654321098765432102"}
	if err = second.saveDeviceRecord(r); err != nil {
		t.Fatal(err)
	}
	if err = second.loadDeviceProfile(); err != nil {
		t.Fatal(err)
	}
	if !second.deviceState.Ready || second.sess.DeviceContext().DeviceID != r.Identity.DeviceID {
		t.Fatal("saved server identity not restored")
	}
	if err = os.WriteFile(filepath.Join(dir, "device.json"), []byte(`{"profile":{"aid":8662}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = second.loadDeviceProfile(); err == nil {
		t.Fatal("invalid stored fingerprint silently replaced")
	}
}
