package fusereadcache

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"litepan/internal/settings"
)

func TestInvalidateRemovesOnlyTargetBlocks(t *testing.T) {
	ctx := context.Background()
	settingSvc, err := settings.New(ctx, &memoryConfigRepo{values: map[string]string{
		"fuse_read_cache_enabled": "true",
	}})
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	svc, err := New(ctx, Options{DataDir: t.TempDir(), Settings: settingSvc})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	data := make([]byte, BlockSize)
	for i := range data {
		data[i] = byte(i % 241)
	}
	fetchCalls := 0
	fetch := func(dest []byte, off int64) (int, error) {
		fetchCalls++
		if n := copy(dest, data[off:]); n < len(dest) {
			return n, io.EOF
		}
		return len(dest), nil
	}
	read := func(file string) {
		t.Helper()
		buf := make([]byte, 128*1024)
		if _, err := svc.ReadAt(ctx, 7, file, buf, 0, fetch); err != nil {
			t.Fatalf("ReadAt(%s): %v", file, err)
		}
	}

	const acct = int64(7)
	acctDir := filepath.Join(svc.store.blocks, strconv.FormatInt(acct, 10))
	dirA := filepath.Join(acctDir, fileDir("fileA"))
	dirB := filepath.Join(acctDir, fileDir("fileB"))

	read("fileA")
	read("fileB")
	for _, d := range []string{dirA, dirB} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("块目录应存在 %s: %v", d, err)
		}
	}

	// 文件级失效：只清目标文件
	if err := svc.InvalidateFile(ctx, acct, "fileA"); err != nil {
		t.Fatalf("InvalidateFile: %v", err)
	}
	if _, err := os.Stat(dirA); !os.IsNotExist(err) {
		t.Fatalf("fileA 块目录应被删除，err=%v", err)
	}
	if _, err := os.Stat(dirB); err != nil {
		t.Fatalf("fileB 块目录不应被连带删除: %v", err)
	}

	before := fetchCalls
	read("fileA")
	if fetchCalls == before {
		t.Fatal("fileA 失效后应重新回源")
	}
	before = fetchCalls
	read("fileB")
	if fetchCalls != before {
		t.Fatal("fileB 不应被连带失效")
	}

	// 账号级失效：只清该账号目录，blocks 根目录必须保留
	if err := svc.InvalidateAccount(ctx, acct); err != nil {
		t.Fatalf("InvalidateAccount: %v", err)
	}
	if _, err := os.Stat(acctDir); !os.IsNotExist(err) {
		t.Fatalf("账号目录应被删除，err=%v", err)
	}
	if _, err := os.Stat(svc.store.blocks); err != nil {
		t.Fatalf("blocks 根目录绝不能被删除: %v", err)
	}
	before = fetchCalls
	read("fileB")
	if fetchCalls == before {
		t.Fatal("账号级失效后应重新回源")
	}
}
