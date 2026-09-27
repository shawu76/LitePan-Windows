package strm

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// 前台浏览目录时会用「目录名 + /」拼出 path 回写缓存。目录名自带斜杠时，
// 这个歧义写法不能把驱动产出的规范路径覆盖掉，否则增强扫描又会把 strm
// 建到错误的多层目录下，并把正确位置的文件当过期删掉。
func TestReconcileDirCacheKeepsCanonicalPath(t *testing.T) {
	svc, _ := testService(t)
	cache := newMemDirCache()
	svc.dirCache = cache
	ctx := context.Background()
	if err := svc.settings.Update(ctx, map[string]string{
		settings.KeyStrmTool115TreeEnabled: "true",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "weird", DirPath: "库/abc_def_ghi"},
		{AccountID: 1, DirID: "renamed", DirPath: "库/旧名字"},
	}); err != nil {
		t.Fatal(err)
	}

	// 浏览进「abc/def/ghi」这个目录：前台传来的 path 是原始拼法，必须保持库里的规范写法。
	svc.ReconcileDirCache(ctx, 1, "weird", "/库/abc/def/ghi", nil)
	if got, _, _ := cache.Get(ctx, 1, "weird"); got != "库/abc_def_ghi" {
		t.Fatalf("规范路径被歧义写法覆盖成了 %q", got)
	}

	// 真改名仍然要能自愈。
	svc.ReconcileDirCache(ctx, 1, "renamed", "/库/新名字", nil)
	if got, _, _ := cache.Get(ctx, 1, "renamed"); got != "库/新名字" {
		t.Fatalf("真改名没有自愈，缓存仍为 %q", got)
	}

	// 名字自带斜杠的子目录不写入缓存，避免制造歧义记录。
	svc.ReconcileDirCache(ctx, 1, "weird", "/库/abc/def/ghi", []domain.FileItem{
		{ID: "child-slash", Name: "abc/def/ghi", IsDir: true},
	})
	if _, ok, _ := cache.Get(ctx, 1, "child-slash"); ok {
		t.Fatal("名字带斜杠的子目录不应写入目录缓存")
	}
}
