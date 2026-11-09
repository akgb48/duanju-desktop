package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type replacementPlan struct {
	PID    int      `json:"pid"`
	Target string   `json:"target"`
	Staged string   `json:"staged"`
	Backup string   `json:"backup"`
	SHA256 string   `json:"sha256"`
	Args   []string `json:"args"`
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func verifyFile(path, expected string) error {
	if !validHash(expected) {
		return errors.New("更新摘要无效")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, maxPackageSize+1)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("待安装文件校验失败")
	}
	return nil
}

func launchReplacement(dir, executable, ready, hash string, args []string) error {
	if err := verifyFile(ready, hash); err != nil {
		return err
	}
	target, err := filepath.Abs(executable)
	if err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(target), ".duanju-update-*.exe")
	if err != nil {
		return fmt.Errorf("程序所在目录不可写，请将程序移到可写目录后重试: %w", err)
	}
	staged := stage.Name()
	stage.Close()
	os.Remove(staged)
	if err = copyFile(ready, staged, 0700); err != nil {
		os.Remove(staged)
		return err
	}
	if err = verifyFile(staged, hash); err != nil {
		os.Remove(staged)
		return err
	}
	helperDir, err := os.MkdirTemp(dir, "apply-")
	if err != nil {
		os.Remove(staged)
		return err
	}
	helper := filepath.Join(helperDir, "duanju-update-helper.exe")
	if err = copyFile(target, helper, 0700); err != nil {
		os.Remove(staged)
		return err
	}
	plan := replacementPlan{PID: os.Getpid(), Target: target, Staged: staged, Backup: staged + ".backup", SHA256: hash, Args: args}
	data, _ := json.Marshal(plan)
	planPath := filepath.Join(helperDir, "plan.json")
	if err = os.WriteFile(planPath, data, 0600); err != nil {
		os.Remove(staged)
		return err
	}
	cmd := exec.Command(helper, "--apply-update", planPath)
	cmd.Dir = filepath.Dir(target)
	hideWindow(cmd)
	if err = cmd.Start(); err != nil {
		os.Remove(staged)
		return err
	}
	return cmd.Process.Release()
}

func RunHelper(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) > 64<<10 {
		return errors.New("更新计划过大")
	}
	var p replacementPlan
	if err = json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err = validatePlan(p); err != nil {
		return err
	}
	if err = verifyFile(p.Staged, p.SHA256); err != nil {
		return err
	}
	if err = waitForParent(p.PID); err != nil {
		return err
	}
	err = replaceAndRestart(p, func(target string, args []string) error {
		cmd := exec.Command(target, args...)
		cmd.Dir = filepath.Dir(target)
		hideWindow(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
	if err != nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(path), "error.txt"), []byte(err.Error()), 0600)
	}
	return err
}

func validatePlan(p replacementPlan) error {
	if p.PID <= 0 || !filepath.IsAbs(p.Target) || !filepath.IsAbs(p.Staged) || !strings.EqualFold(filepath.Ext(p.Target), ".exe") || !validHash(p.SHA256) {
		return errors.New("更新计划无效")
	}
	if filepath.Dir(p.Target) != filepath.Dir(p.Staged) || p.Backup != p.Staged+".backup" || !strings.HasPrefix(filepath.Base(p.Staged), ".duanju-update-") || p.Target == p.Staged {
		return errors.New("更新目标路径无效")
	}
	for _, path := range []string{p.Target, p.Staged} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("更新目标必须是普通文件")
		}
	}
	return nil
}

func replaceAndRestart(p replacementPlan, start func(string, []string) error) error {
	if err := verifyFile(p.Staged, p.SHA256); err != nil {
		return err
	}
	if err := os.Rename(p.Target, p.Backup); err != nil {
		return fmt.Errorf("备份程序失败: %w", err)
	}
	rollback := func(cause error) error {
		_ = os.Remove(p.Target)
		if err := os.Rename(p.Backup, p.Target); err != nil {
			return fmt.Errorf("更新失败且恢复失败: %v; %w", cause, err)
		}
		_ = start(p.Target, p.Args)
		return cause
	}
	if err := os.Rename(p.Staged, p.Target); err != nil {
		return rollback(err)
	}
	if err := start(p.Target, p.Args); err != nil {
		return rollback(fmt.Errorf("启动新版本失败，已恢复旧版: %w", err))
	}
	_ = os.Remove(p.Backup)
	return nil
}
