package multi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStudioLeaseCancelsMissingOrExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease")
	if e := os.WriteFile(path, []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitorStudioLease(ctx, cancel, path, 30*time.Millisecond, 5*time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expired lease failed to cancel")
	}
}
func TestStudioLeaseDoesNotCancelHealthy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease")
	if e := os.WriteFile(path, []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitorStudioLease(ctx, cancel, path, 200*time.Millisecond, 5*time.Millisecond)
	for i := 0; i < 4; i++ {
		time.Sleep(15 * time.Millisecond)
		if e := os.WriteFile(path, []byte("ok"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	select {
	case <-ctx.Done():
		t.Fatal("active lease should not cancel")
	default:
	}
}
