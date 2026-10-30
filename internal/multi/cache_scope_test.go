package multi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentDriverCacheScope(t *testing.T) {
	root := t.TempDir()
	p := &project{dir: root, stage: filepath.Join(root, "stage"), cfg: Config{DriverCacheScope: "project"}}
	a, e := p.driverCacheDirectory()
	if e != nil || a != filepath.Join(root, ".allocation-driver-cache") {
		t.Fatal(a, e)
	}
	p.cfg.DriverCacheScope = "run"
	b, e := p.driverCacheDirectory()
	if e != nil || a == b {
		t.Fatal(a, b, e)
	}
}
func TestPersistentCacheSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "real")
	os.Mkdir(dst, 0700)
	if e := os.Symlink(dst, filepath.Join(root, ".allocation-driver-cache")); e != nil {
		t.Skip(e)
	}
	p := &project{dir: root, cfg: Config{DriverCacheScope: "project"}}
	if _, e := p.driverCacheDirectory(); e == nil {
		t.Fatal("symlink permitted")
	}
}
