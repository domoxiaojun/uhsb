package cfcommon

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Progress 用独立 goroutine 打印进度条,worker 只调 Inc/Success。
// 避免多 worker 并发写 stdout 的错乱。
type ProgressHandler func(done, total, hit int64, latest string)

type Progress struct {
	total     int64
	done      atomic.Int64
	hit       atomic.Int64
	lastLine  string
	lineMux   sync.Mutex
	stopCh    chan struct{}
	stoppedCh chan struct{}
	handler   ProgressHandler
}

// NewProgress 启动 ticker goroutine,每 interval 刷新一行。
// 使用完必须调用 Stop。
func NewProgress(total int, interval time.Duration) *Progress {
	return NewProgressWithHandler(total, interval, nil)
}

func NewProgressWithHandler(total int, interval time.Duration, handler ProgressHandler) *Progress {
	p := &Progress{
		total:     int64(total),
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
		handler:   handler,
	}
	go p.loop(interval)
	return p
}

// Inc 任务完成计数 +1。
func (p *Progress) Inc() { p.done.Add(1) }

// Hit 命中计数 +1,latest 记录最新一条信息(供下次刷新显示)。
func (p *Progress) Hit(latest string) {
	p.hit.Add(1)
	if latest != "" {
		p.lineMux.Lock()
		p.lastLine = latest
		p.lineMux.Unlock()
	}
}

// Stop 停止 ticker 并打印最后一行 + 换行。
func (p *Progress) Stop() {
	close(p.stopCh)
	<-p.stoppedCh
}

// Snapshot 返回 (done, hit)。
func (p *Progress) Snapshot() (int64, int64) {
	return p.done.Load(), p.hit.Load()
}

func (p *Progress) loop(interval time.Duration) {
	defer close(p.stoppedCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		p.print()
		select {
		case <-ticker.C:
		case <-p.stopCh:
			p.print()
			if p.handler == nil {
				fmt.Println()
			}
			return
		}
	}
}

func (p *Progress) print() {
	d := p.done.Load()
	h := p.hit.Load()
	p.lineMux.Lock()
	line := p.lastLine
	p.lineMux.Unlock()
	if p.handler != nil {
		p.handler(d, p.total, h, line)
		return
	}
	pct := 0.0
	if p.total > 0 {
		pct = float64(d) / float64(p.total) * 100
	}
	if line != "" {
		fmt.Printf("\r进度: %d/%d (%.0f%%) | 命中: %d | %s\033[K",
			d, p.total, pct, h, line)
	} else {
		fmt.Printf("\r进度: %d/%d (%.0f%%) | 命中: %d\033[K",
			d, p.total, pct, h)
	}
}
