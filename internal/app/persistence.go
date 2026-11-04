package app

import (
	"context"
	"duanju-desktop/pkg/model"
	"duanju-desktop/pkg/store"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var memSeries = map[string]model.Series{}
var localSaveMu sync.Mutex

func writeFileAtomically(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, path)
}

type localSnapshot struct {
	Settings     model.Settings                `json:"settings"`
	Series       map[string]model.Series       `json:"series"`
	Favorites    map[string]model.Series       `json:"favorites"`
	History      map[string]model.HistoryEntry `json:"history"`
	Progress     map[string]PlayState          `json:"progress"`
	Reservations map[string]model.Series       `json:"reservations"`
}

func (a *App) rememberSeries(s model.Series) {
	memMu.Lock()
	memSeries[s.ID] = s
	if _, ok := memFavorites[s.ID]; ok {
		memFavorites[s.ID] = s
	}
	memMu.Unlock()
}

func (a *App) persistLocal() error {
	localSaveMu.Lock()
	defer localSaveMu.Unlock()
	a.mu.RLock()
	st := a.st
	a.mu.RUnlock()
	if st == nil {
		return errors.New("本地存储未就绪")
	}
	memMu.RLock()
	data, err := json.Marshal(localSnapshot{Settings: memSettings, Series: memSeries, Favorites: memFavorites, History: memHistory, Progress: memProgress, Reservations: memReservations})
	memMu.RUnlock()
	if err != nil {
		return err
	}
	return st.SetKV(context.Background(), "local.state.v1", string(data))
}

func (a *App) restoreLocal() error {
	if a.st == nil {
		return errors.New("本地存储未就绪")
	}
	data, err := a.st.GetKV(context.Background(), "local.state.v1")
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	v := localSnapshot{Settings: model.DefaultSettings()}
	if err = json.Unmarshal([]byte(data), &v); err != nil {
		return err
	}
	memMu.Lock()
	memSettings = v.Settings
	if v.Series != nil {
		memSeries = v.Series
	}
	if v.Favorites != nil {
		memFavorites = v.Favorites
	}
	if v.History != nil {
		memHistory = v.History
	}
	if v.Progress != nil {
		memProgress = v.Progress
	}
	if v.Reservations != nil {
		memReservations = v.Reservations
	}
	memMu.Unlock()
	if a.sess != nil {
		a.sess.SetProxy(v.Settings.ProxyURL)
	}
	return nil
}
