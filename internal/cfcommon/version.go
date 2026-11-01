package cfcommon

import (
	"fmt"
	"os"
)

// Version 是整个 cftest 工具链的共享版本号。
const Version = "0.4.1"

// PrintVersionAndExit 输出 "<tool> <Version>" 到 stdout 后以 0 退出。
// 每个子命令在 flag.Parse() 后紧接着检查 -version 即可:
//
//	if *versionFlag { cfcommon.PrintVersionAndExit("cfspeed") }
func PrintVersionAndExit(tool string) {
	fmt.Printf("%s %s\n", tool, Version)
	os.Exit(0)
}
