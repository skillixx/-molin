package videosecrets_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"molin/server/internal/config/videosecrets"
)

func secretFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "payload")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, path
}

func loadPayload(repo, path string) (*videosecrets.Bundle, error) {
	return videosecrets.Load(repo, []videosecrets.File{{Purpose: videosecrets.Payload, Path: path}})
}

func TestVideoG7SecretLinuxReadAndRedaction(t *testing.T) {
	repo, path := secretFixture(t)
	for _, mode := range []os.FileMode{0o400, 0o600} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		bundle, err := loadPayload(repo, path)
		if err != nil {
			t.Fatal(err)
		}
		value, ok := bundle.Bytes(videosecrets.Payload)
		if !ok || string(value) != strings.Repeat("a", 32) {
			t.Fatal("安全文件必须按用途加载原值")
		}
		value[0] = 'z'
		again, _ := bundle.Bytes(videosecrets.Payload)
		if again[0] != 'a' {
			t.Fatal("调用者不能改写凭据快照")
		}
		for _, printable := range []any{bundle, *bundle} {
			encoded, err := json.Marshal(printable)
			if err != nil || string(encoded) != `"[REDACTED]"` {
				t.Fatal("凭据包及其值副本必须阻止普通JSON泄漏")
			}
			for _, formatted := range []string{fmt.Sprintf("%v", printable), fmt.Sprintf("%+v", printable), fmt.Sprintf("%#v", printable)} {
				if formatted != "[视频凭据已脱敏]" {
					t.Fatal("格式化输出只能包含固定标记，不能泄漏文本或字节数组")
				}
			}
		}
		if _, ok := bundle.Bytes(videosecrets.Callback); ok {
			t.Fatal("未注册用途不能读到其他用途的凭据")
		}
	}
}

func TestVideoG7SecretLinuxUnsafeFileMatrix(t *testing.T) {
	for _, name := range []string{"仓库内", "不存在", "目录", "符号链接", "父目录链接", "仓库根链接", "硬链接", "FIFO", "0640", "0644", "0700", "空文件", "短AES", "长AES", "超限", "前空格", "尾空格", "内换行", "Unicode空白"} {
		t.Run(name, func(t *testing.T) {
			repo, path := secretFixture(t)
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "仓库内":
				path = filepath.Join(repo, "key")
				check(os.WriteFile(path, []byte(strings.Repeat("a", 32)), 0o600))
			case "不存在":
				path += ".missing"
			case "目录":
				path = filepath.Dir(path)
			case "符号链接":
				link := path + ".link"
				check(os.Symlink(path, link))
				path = link
			case "父目录链接":
				link := filepath.Join(filepath.Dir(path), "alias")
				check(os.Symlink(filepath.Dir(path), link))
				path = filepath.Join(link, "payload")
			case "仓库根链接":
				link := repo + ".link"
				check(os.Symlink(repo, link))
				repo = link
			case "硬链接":
				check(os.Link(path, filepath.Join(repo, "alias")))
			case "FIFO":
				path += ".fifo"
				check(syscall.Mkfifo(path, 0o600))
			case "0640":
				check(os.Chmod(path, 0o640))
			case "0644":
				check(os.Chmod(path, 0o644))
			case "0700":
				check(os.Chmod(path, 0o700))
			default:
				contents := map[string]string{"空文件": "", "短AES": strings.Repeat("a", 31), "长AES": strings.Repeat("a", 33), "超限": strings.Repeat("a", 8193), "前空格": " " + strings.Repeat("a", 31), "尾空格": strings.Repeat("a", 31) + " ", "内换行": strings.Repeat("a", 15) + "\n" + strings.Repeat("a", 16), "Unicode空白": strings.Repeat("a", 29) + "\u3000"}
				check(os.WriteFile(path, []byte(contents[name]), 0o600))
			}
			bundle, err := loadPayload(repo, path)
			if bundle != nil || !errors.Is(err, videosecrets.ErrInvalid) {
				t.Fatal("不安全文件必须整包失败关闭")
			}
			if strings.Contains(err.Error(), path) {
				t.Fatal("错误不能泄漏路径")
			}
		})
	}
}

func TestVideoG7SecretLinuxPurposeIsolation(t *testing.T) {
	repo, path := secretFixture(t)
	second := path + ".second"
	if err := os.WriteFile(second, []byte(strings.Repeat("b", 32)+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []videosecrets.File{{Purpose: videosecrets.Payload, Path: path}, {Purpose: videosecrets.Quote, Path: second}}
	bundle, err := videosecrets.Load(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	quote, ok := bundle.Bytes(videosecrets.Quote)
	if !ok || string(quote) != strings.Repeat("b", 32) {
		t.Fatal("应允许凭据末尾换行并隔离用途")
	}
	for _, bad := range [][]videosecrets.File{
		nil,
		{{Purpose: videosecrets.Payload, Path: path}, {Purpose: videosecrets.Payload, Path: second}},
		{{Purpose: videosecrets.Payload, Path: path}, {Purpose: videosecrets.Quote, Path: path}},
		{{Purpose: videosecrets.Purpose("provider_key"), Path: path}},
		{{Purpose: videosecrets.Payload, Path: path}, {Purpose: videosecrets.Callback, Path: path + ".missing"}},
	} {
		if partial, err := videosecrets.Load(repo, bad); partial != nil || !errors.Is(err, videosecrets.ErrInvalid) {
			t.Fatal("重复用途、共享路径或部分读取失败不能返回凭据包")
		}
	}
	if err := os.WriteFile(second, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if reused, err := videosecrets.Load(repo, files); reused != nil || !errors.Is(err, videosecrets.ErrInvalid) {
		t.Fatal("不同文件存放相同密钥仍属于用途复用，必须拒绝")
	}
}

func TestVideoG7SecretLinuxSymlinkReplacementRace(t *testing.T) {
	repo, path := secretFixture(t)
	forbidden := filepath.Join(repo, "forbidden")
	if err := os.WriteFile(forbidden, []byte(strings.Repeat("z", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			next := path + ".next"
			if err := os.Symlink(forbidden, next); err != nil {
				done <- err
				return
			}
			if err := os.Rename(next, path); err != nil {
				done <- err
				return
			}
			if err := os.WriteFile(next, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
				done <- err
				return
			}
			if err := os.Rename(next, path); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 200; i++ {
		bundle, err := loadPayload(repo, path)
		if err != nil {
			if !errors.Is(err, videosecrets.ErrInvalid) {
				t.Error("竞态错误必须低敏失败关闭")
			}
			continue
		}
		value, _ := bundle.Bytes(videosecrets.Payload)
		if string(value) != strings.Repeat("a", 32) {
			t.Error("符号链接替换不能越过仓库边界读取禁止内容")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
