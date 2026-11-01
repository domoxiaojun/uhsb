package cfcommon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

// HandleInterrupt 注册 SIGINT 处理,首次收到信号时调用 cancel() 并打印提示。
// 返回的 stop 函数用于撤销监听(调用方 defer)。
func HandleInterrupt(cancel context.CancelFunc, msg string) (stop func()) {
	return HandleInterruptFunc(cancel, msg, nil)
}

// HandleInterruptFunc 在首次收到 SIGINT 时执行 onInterrupt 然后 cancel。
func HandleInterruptFunc(cancel context.CancelFunc, msg string, onInterrupt func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		if _, ok := <-ch; !ok {
			return
		}
		if msg != "" {
			fmt.Printf("\n%s\n", msg)
		}
		if onInterrupt != nil {
			onInterrupt()
		}
		cancel()
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}
