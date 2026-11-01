package cfcommon

import (
	"os"
	"path/filepath"
)

func ReplaceFileFromTemp(tempPath, targetPath string) error {
	tempPath = filepath.Clean(tempPath)
	targetPath = filepath.Clean(targetPath)
	if err := ensureParentDir(targetPath); err != nil {
		return wrapPathError("prepare output path", targetPath, err)
	}

	backupPath := targetPath + ".bak"
	_ = os.Remove(backupPath)

	if _, err := os.Stat(targetPath); err == nil {
		if err := os.Rename(targetPath, backupPath); err != nil {
			return wrapPathError("backup file", targetPath, err)
		}
		if err := os.Rename(tempPath, targetPath); err != nil {
			_ = os.Rename(backupPath, targetPath)
			return wrapPathError("replace file", targetPath, err)
		}
		_ = os.Remove(backupPath)
		return nil
	}

	return wrapPathError("replace file", targetPath, os.Rename(tempPath, targetPath))
}

func TempWritePath(path string) string {
	path = filepath.Clean(path)
	return path + ".tmp"
}
