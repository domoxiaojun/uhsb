package cfcommon

import "context"

// AcquireSemaphore 在获取并发槽时响应取消。
func AcquireSemaphore(ctx context.Context, sem chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
