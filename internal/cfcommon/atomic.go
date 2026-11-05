package cfcommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	tempFileSeq uint64
	// Windows 上 os.Rename 不能覆盖已存在文件，需要「备份+两次 rename」。
	// 这一整段必须串行，否则并发写同一目标时会互相把对方的备份挪走。
	replaceMu sync.Mutex
)

// TempWritePath 返回同目录内唯一、不可预测的临时文件名。
// 固定 ".tmp" 会被并发写同一目标的两个任务互相覆盖；唯一名保证各写各的。
func TempWritePath(path string) string {
	path = filepath.Clean(path)
	n := atomic.AddUint64(&tempFileSeq, 1)
	return fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), n)
}

// FinishCSVWrite 收尾一次「写临时 CSV → close/flush → 原子替换」。
//
// 刻意不使用 `writeErr != nil || closeFn() != nil || Replace... != nil` 这种
// 短路写法：短路会在写失败时跳过 Close（csv.Writer 不 flush、fd 泄漏），
// 也跳过 ReplaceFileFromTemp 里的临时文件清理，且函数无返回值让调用方
// 误以为保存成功。这里无条件 close，聚合所有错误，失败一律显式上报。
func FinishCSVWrite(tempPath, targetPath string, writeErr error, closeFn func() error) error {
	closeErr := closeFn()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return ReplaceFileFromTemp(tempPath, targetPath)
}

// ReplaceFileFromTemp 把临时文件原子替换到目标路径。
// 优先单次 rename（Linux 直接覆盖）；目标已存在且 rename 失败时（Windows）
// 才走「唯一备份名 + 两次 rename」，并在失败时回滚。
func ReplaceFileFromTemp(tempPath, targetPath string) error {
	replaceMu.Lock()
	defer replaceMu.Unlock()
	tempPath = filepath.Clean(tempPath)
	targetPath = filepath.Clean(targetPath)
	defer func() { _ = os.Remove(tempPath) }() // 失败时清理，成功时文件已不存在
	if err := ensureParentDir(targetPath); err != nil {
		return wrapPathError("prepare output path", targetPath, err)
	}

	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	}

	backupPath := fmt.Sprintf("%s.%d.%d.bak", targetPath, os.Getpid(), atomic.AddUint64(&tempFileSeq, 1))
	if _, statErr := os.Stat(targetPath); statErr == nil {
		if err := os.Rename(targetPath, backupPath); err != nil {
			return wrapPathError("backup file", targetPath, err)
		}
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Rename(backupPath, targetPath)
		return wrapPathError("replace file", targetPath, err)
	}
	_ = os.Remove(backupPath)
	return nil
}
