package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// writeFileAtomic writes data to path after the payload is fully prepared.
// It first writes a unique sibling temp file, Syncs it, then replaces the
// destination. If rename cannot replace a Docker file bind-mount (EBUSY) or
// otherwise fails, it falls back to truncating and writing the existing inode
// so the host-mounted file still receives the bytes.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	if perm == 0 {
		perm = 0o600
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".cliproxy-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := replacePath(tmpName, path); err != nil {
		if inPlaceErr := writeFileInPlace(path, data, perm); inPlaceErr != nil {
			return fmt.Errorf("replace config file: %w (in-place: %v)", err, inPlaceErr)
		}
		return nil
	}
	cleanup = false
	return nil
}

func writeFileInPlace(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func replacePath(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dst)
	}
	bak := dst + ".replace-bak"
	_ = os.Remove(bak)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, bak); err != nil {
			if err2 := os.Remove(dst); err2 != nil {
				return err
			}
		}
	}
	if err := os.Rename(src, dst); err != nil {
		if _, statErr := os.Stat(bak); statErr == nil {
			_ = os.Rename(bak, dst)
		}
		return err
	}
	_ = os.Remove(bak)
	return nil
}
