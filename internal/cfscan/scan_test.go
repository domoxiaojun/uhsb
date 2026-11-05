package cfscan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildMTROutput(hops int) string {
	var b strings.Builder
	b.WriteString("HOST: example                       Loss%   Snt   Last   Avg  Best  Wrst StDev\n")
	for i := 1; i <= hops; i++ {
		fmt.Fprintf(&b, " %2d.|-- 10.0.%d.1                     0.0%%     2    1.0   1.0   1.0   1.0   0.0\n", i, i)
	}
	return b.String()
}

// fakeScript 生成一个最小可执行脚本。Windows 上无扩展名的 shebang 文件无法
// 直接 exec，改用 .bat（Go 的 os/exec 会自动交给 cmd.exe），Unix 用 shebang。
func fakeScript(t *testing.T, name, stdout, stderr string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	var path, body string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, name+".bat")
		if stdout != "" {
			body += "@echo " + stdout + "\r\n"
		}
		if stderr != "" {
			body += "@echo " + stderr + " 1>&2\r\n"
		}
		body += fmt.Sprintf("@exit /b %d\r\n", exitCode)
	} else {
		path = filepath.Join(dir, name)
		if stdout != "" {
			body += "echo '" + stdout + "'\n"
		}
		if stderr != "" {
			body += "echo '" + stderr + "' >&2\n"
		}
		body += fmt.Sprintf("exit %d\n", exitCode)
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClassifyMTRKeywordHit(t *testing.T) {
	output := buildMTROutput(3) + "  4.|-- hkg-core.example.net          0.0%     2    1.0   1.0   1.0   1.0   0.0\n"
	res, ok := classifyMTR(output, "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected keyword hit")
	}
	if !strings.Contains(res.Keyword, "hkg") {
		t.Fatalf("keyword = %q, want hkg", res.Keyword)
	}
	if res.HopCount != 4 {
		t.Fatalf("hopCount = %d, want 4", res.HopCount)
	}
}

func TestClassifyMTRHighQualityByHops(t *testing.T) {
	res, ok := classifyMTR(buildMTROutput(20), "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected high-quality hit")
	}
	if !strings.Contains(res.Keyword, "high-quality") {
		t.Fatalf("keyword = %q, want high-quality", res.Keyword)
	}
	if res.HopCount != 20 {
		t.Fatalf("hopCount = %d, want 20", res.HopCount)
	}
}

func TestClassifyMTRHighQualityDisabled(t *testing.T) {
	if _, ok := classifyMTR(buildMTROutput(20), "192.0.2.1", -1); ok {
		t.Fatal("high-quality heuristic should be disabled")
	}
}

func TestClassifyMTRCMIDirect(t *testing.T) {
	output := buildMTROutput(2) + fmt.Sprintf("  3.|-- %s                       0.0%%     2    1.0   1.0   1.0   1.0   0.0\n", CMIHK)
	res, ok := classifyMTR(output, "192.0.2.1", DefaultHighQualityHops)
	if !ok {
		t.Fatal("expected CMI hit")
	}
	if !strings.Contains(res.Keyword, "cmi-hk") {
		t.Fatalf("keyword = %q, want cmi-hk", res.Keyword)
	}
}

func TestClassifyMTRNoMatch(t *testing.T) {
	if _, ok := classifyMTR(buildMTROutput(3), "192.0.2.1", DefaultHighQualityHops); ok {
		t.Fatal("expected no match for plain short route")
	}
}

func TestExtractLossAvg(t *testing.T) {
	loss, avg := ExtractLossAvg("  3.|-- 1.1.1.1  0.0%  2  1.0  1.2  1.0  1.4  0.1")
	if loss != "0.0" {
		t.Fatalf("loss = %q", loss)
	}
	if avg == "" {
		t.Fatal("avg should not be empty")
	}
}

func TestProbeMTRRejectsFailingBinary(t *testing.T) {
	bin := fakeScript(t, "fakemtr", "", "mtr: permission denied", 1)
	if err := probeMTR(bin); err == nil {
		t.Fatal("expected probe error for failing mtr")
	}
}

func TestProbeMTRAcceptsWorkingBinary(t *testing.T) {
	bin := fakeScript(t, "fakemtr", "HOST: example", "", 0)
	if err := probeMTR(bin); err != nil {
		t.Fatalf("probeMTR: %v", err)
	}
}

// ctx 必须真正传到 mtr 子进程，否则「停止任务」后探测还会挂着 8 秒。
func TestProbeMTRCtxCanceled(t *testing.T) {
	bin := fakeScript(t, "fakemtr", "HOST: example", "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := probeMTRCtx(ctx, bin)
	if err == nil {
		t.Fatal("canceled context should abort the probe")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("canceled probe still blocked for %v", time.Since(start))
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cancel") {
		t.Fatalf("err = %v, want a context-canceled error", err)
	}
}

func TestCompileKeywordsCustom(t *testing.T) {
	re, err := CompileKeywords("nrt,sin")
	if err != nil {
		t.Fatalf("CompileKeywords: %v", err)
	}
	nrt := buildMTROutput(2) + "  3.|-- nrt-core.example.net   0.0%     2    1.0   1.0   1.0   1.0   0.0\n"
	res, ok := classifyMTRWithKeywords(nrt, "192.0.2.1", DefaultHighQualityHops, re)
	if !ok || !strings.Contains(res.Keyword, "nrt") {
		t.Fatalf("custom keyword should hit nrt: ok=%v keyword=%q", ok, res.Keyword)
	}
	// 覆盖后默认港台关键字不应再命中，避免继续靠高跳数兜底。
	hkg := buildMTROutput(2) + "  3.|-- hkg-core.example.net   0.0%     2    1.0   1.0   1.0   1.0   0.0\n"
	if _, ok := classifyMTRWithKeywords(hkg, "192.0.2.1", DefaultHighQualityHops, re); ok {
		t.Fatal("hkg should not match after overriding keywords with nrt,sin")
	}
}

func TestCompileKeywordsEmptyFallsBackToDefault(t *testing.T) {
	re, err := CompileKeywords("  ")
	if err != nil {
		t.Fatalf("CompileKeywords: %v", err)
	}
	if re.String() != defaultKeywordRegex.String() {
		t.Fatalf("empty spec should fall back to default regex, got %q", re.String())
	}
}

func TestCompileKeywordsRejectsInvalid(t *testing.T) {
	for _, bad := range []string{"nrt|sin", "(foo", "a.*b", "nrt,香港"} {
		if _, err := CompileKeywords(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

// 回归：/api/status 断连产生的 ctx 取消不能被缓存成「mtr 不可用」，
// 否则之后 1 分钟内所有任务都会误跳过 cfscan。
func TestMTRStatusCtxCancelDoesNotPoisonCache(t *testing.T) {
	bin := fakeScript(t, "mtr", "HOST: example", "", 0)
	t.Setenv("PATH", filepath.Dir(bin))
	resetMTRProbe()
	defer resetMTRProbe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := MTRStatusCtx(ctx)
	if err == nil || strings.Contains(err.Error(), "mtr 未安装") {
		t.Fatalf("用例前置条件不满足（找不到 fake mtr）: %v", err)
	}
	mtrProbeMu.Lock()
	fresh := mtrProbeAt.IsZero()
	mtrProbeMu.Unlock()
	if !fresh {
		t.Fatal("ctx 取消的探测失败被写入缓存")
	}
	if _, err := MTRStatus(); err != nil {
		t.Fatalf("取消之后正常探测应成功: %v", err)
	}
}
