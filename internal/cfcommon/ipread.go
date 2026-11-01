package cfcommon

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ReadIPs 从文件读取 IP,支持 # 整行注释和行尾注释,空行跳过。
func ReadIPs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ips []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			ips = append(ips, line)
		}
	}
	return ips, s.Err()
}

// WriteLines 将 lines 逐行写入文件(以 \n 分隔,末尾 \n)。
func WriteLines(path string, lines []string) error {
	path = filepath.Clean(path)
	if err := ensureParentDir(path); err != nil {
		return wrapPathError("prepare output path", path, err)
	}
	return wrapPathError("write file", path, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644))
}

func WriteLinesAtomic(path string, lines []string) error {
	tempPath := TempWritePath(path)
	if err := WriteLines(tempPath, lines); err != nil {
		return err
	}
	return ReplaceFileFromTemp(tempPath, path)
}
