package cfcommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTempWritePathIsUniqueAndInSameDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "result.txt")
	a, b := TempWritePath(target), TempWritePath(target)
	if a == b {
		t.Fatalf("two temp paths collided: %s", a)
	}
	if filepath.Dir(a) != dir || filepath.Dir(b) != dir {
		t.Fatalf("temp files must live next to the target for rename to be atomic: %s %s", a, b)
	}
}

func TestWriteLinesAtomicOverwritesWithoutLeftovers(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "result.txt")
	if err := WriteLinesAtomic(target, []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLinesAtomic(target, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second\n" {
		t.Fatalf("content = %q, want second\\n", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("leftover temp/backup files: %v", entries)
	}
}

// 并发写同一目标时不能互相踩踏：结果必须是某个写入者的完整内容，且不留临时文件。
func TestWriteLinesAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "result.txt")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := WriteLinesAtomic(target, []string{fmt.Sprintf("writer-%d", n)}); err != nil {
				t.Errorf("writer %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var valid bool
	for i := 0; i < 20; i++ {
		if string(got) == fmt.Sprintf("writer-%d\n", i) {
			valid = true
			break
		}
	}
	if !valid {
		t.Fatalf("torn write detected: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("leftover temp/backup files: %v", entries)
	}
}

// 回归：三处 CSV 保存曾用 `writeErr != nil || closeFn() != nil` 短路，
// 写失败时既不 Close 也不清临时文件，还静默返回成功。
func TestFinishCSVWriteAlwaysClosesAndCleans(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "x.csv.tmp")
	if err := os.WriteFile(temp, []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	closed := false
	err := FinishCSVWrite(temp, filepath.Join(dir, "x.csv"), fmt.Errorf("write boom"), func() error {
		closed = true
		return fmt.Errorf("close boom")
	})
	if err == nil {
		t.Fatal("FinishCSVWrite should return an error")
	}
	if !closed {
		t.Fatal("closeFn 被短路跳过：csv.Writer 未 flush、fd 泄漏")
	}
	if _, statErr := os.Stat(temp); !os.IsNotExist(statErr) {
		t.Fatal("失败后临时文件未清理")
	}
	if !strings.Contains(err.Error(), "write boom") || !strings.Contains(err.Error(), "close boom") {
		t.Fatalf("错误未聚合: %v", err)
	}
}
