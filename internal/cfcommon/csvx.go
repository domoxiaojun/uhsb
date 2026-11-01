package cfcommon

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
)

// NewCSVWriter 创建一个 UTF-8 BOM 开头的 CSV 文件。
// 返回 writer、底层 file 和 close 函数。调用方 defer close()。
func NewCSVWriter(path string) (*csv.Writer, *os.File, func() error, error) {
	path = filepath.Clean(path)
	if err := ensureParentDir(path); err != nil {
		return nil, nil, nil, wrapPathError("prepare csv path", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, nil, nil, wrapPathError("open csv", path, err)
	}
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		_ = f.Close()
		return nil, nil, nil, wrapPathError("write csv bom", path, err)
	}
	w := csv.NewWriter(f)
	closeFn := func() error {
		w.Flush()
		if err := w.Error(); err != nil {
			_ = f.Close()
			return wrapPathError("flush csv", path, err)
		}
		if err := f.Close(); err != nil {
			return wrapPathError("close csv", path, err)
		}
		return nil
	}
	return w, f, closeFn, nil
}

// NewCSVAppendWriter 以追加模式打开 CSV。
// 文件不存在时写入 UTF-8 BOM 和 header；存在时仅追加数据行。
func NewCSVAppendWriter(path string, header []string) (*csv.Writer, *os.File, func() error, bool, error) {
	path = filepath.Clean(path)
	if err := ensureParentDir(path); err != nil {
		return nil, nil, nil, false, wrapPathError("prepare csv path", path, err)
	}

	_, statErr := os.Stat(path)
	fileExists := statErr == nil

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, nil, false, wrapPathError("open csv", path, err)
	}
	if !fileExists {
		if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			_ = f.Close()
			return nil, nil, nil, false, wrapPathError("write csv bom", path, err)
		}
	}

	w := csv.NewWriter(f)
	if !fileExists && len(header) > 0 {
		if err := w.Write(header); err != nil {
			_ = f.Close()
			return nil, nil, nil, false, wrapPathError("write csv header", path, err)
		}
	}

	closeFn := func() error {
		w.Flush()
		if err := w.Error(); err != nil {
			_ = f.Close()
			return wrapPathError("flush csv", path, err)
		}
		if err := f.Close(); err != nil {
			return wrapPathError("close csv", path, err)
		}
		return nil
	}
	return w, f, closeFn, !fileExists, nil
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func wrapPathError(action, path string, err error) error {
	if err == nil {
		return nil
	}
	if abs, absErr := filepath.Abs(path); absErr == nil {
		return fmt.Errorf("%s %q: %w", action, abs, err)
	}
	return fmt.Errorf("%s %q: %w", action, path, err)
}
