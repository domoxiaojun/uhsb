package cfcommon

import (
	"context"
	"testing"
)

func TestAcquireSemaphoreHonorsCancellation(t *testing.T) {
	sem := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got := AcquireSemaphore(ctx, sem); got {
		t.Fatalf("AcquireSemaphore() = true, want false")
	}
	if len(sem) != 0 {
		t.Fatalf("semaphore len = %d, want 0", len(sem))
	}
}

func TestAcquireSemaphoreAcquiresSlot(t *testing.T) {
	sem := make(chan struct{}, 1)
	if got := AcquireSemaphore(context.Background(), sem); !got {
		t.Fatalf("AcquireSemaphore() = false, want true")
	}
	if len(sem) != 1 {
		t.Fatalf("semaphore len = %d, want 1", len(sem))
	}
}
