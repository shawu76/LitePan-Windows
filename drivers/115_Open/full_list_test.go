package pan115open

import "testing"

// 目录名里的 "/" 不能被拼成路径层级：上层（STRM 扫描）靠 "/" 分段，
// 一旦名字里带斜杠，它就会把一个目录当成多层目录，把本地 strm 建错位置，
// 并把正确位置的文件当过期清理掉。
func TestBuildDirPathKeepsSlashInsideName(t *testing.T) {
	paths := []dirPathEntry{
		{FileID: flexString("0"), FileName: "账号根"},
		{FileID: flexString("100"), FileName: "库"},
	}
	got, ok := buildDirPath(paths, "abc/def/ghi", "100")
	if !ok {
		t.Fatal("应能裁到挂载根")
	}
	if got != "abc_def_ghi" {
		t.Fatalf("路径=%q，期望 abc_def_ghi（名字里的斜杠不能变成一层）", got)
	}
}

func TestBuildDirPathSanitizesIntermediateNames(t *testing.T) {
	paths := []dirPathEntry{
		{FileID: flexString("100"), FileName: "库"},
		{FileID: flexString("200"), FileName: "合集/上"},
	}
	got, ok := buildDirPath(paths, "影片.mkv", "100")
	if !ok {
		t.Fatal("应能裁到挂载根")
	}
	if got != "合集_上/影片.mkv" {
		t.Fatalf("路径=%q，期望 合集_上/影片.mkv", got)
	}
}

// 正常的层级必须原样保留。
func TestBuildDirPathKeepsRealLayers(t *testing.T) {
	paths := []dirPathEntry{
		{FileID: flexString("100"), FileName: "库"},
		{FileID: flexString("200"), FileName: "电影"},
	}
	got, ok := buildDirPath(paths, "2024", "100")
	if !ok {
		t.Fatal("应能裁到挂载根")
	}
	if got != "电影/2024" {
		t.Fatalf("路径=%q，期望 电影/2024", got)
	}
}

// 挂载根以上的段要被裁掉（根不是 0 的账号）。
func TestBuildDirPathTrimsAboveMountRoot(t *testing.T) {
	paths := []dirPathEntry{
		{FileID: flexString("0"), FileName: "账号根"},
		{FileID: flexString("50"), FileName: "某个上层目录"},
		{FileID: flexString("100"), FileName: "库"},
		{FileID: flexString("200"), FileName: "电影"},
	}
	got, ok := buildDirPath(paths, "2024", "100")
	if !ok {
		t.Fatal("应能裁到挂载根")
	}
	if got != "电影/2024" {
		t.Fatalf("路径=%q，期望 电影/2024（挂载根以上的段应被裁掉）", got)
	}
}

func TestBuildDirPathFailsWhenRootMissing(t *testing.T) {
	paths := []dirPathEntry{{FileID: flexString("0"), FileName: "账号根"}}
	if _, ok := buildDirPath(paths, "影片.mkv", "100"); ok {
		t.Fatal("挂载根不在父链里时应返回 false")
	}
}
