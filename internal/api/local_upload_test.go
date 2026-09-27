package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRelativePath(t *testing.T) {
	cases := map[string]string{
		"媒体库/电影.mkv":       "媒体库/电影.mkv",
		"/媒体库/电影.mkv":      "媒体库/电影.mkv",
		"媒体库\\电影.mkv":      "媒体库/电影.mkv",
		"../秘密/偷跑.mkv":     "",
		"../../etc/passwd": "",
		"媒体库/../电影":        "电影",
		"":                 "",
		"/":                "",
	}
	for in, want := range cases {
		if got := cleanRelativePath(in); got != want {
			t.Errorf("cleanRelativePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveLocalUploadSourceRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.mkv")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.mkv")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link.mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveLocalUploadSourceUnderRoot(inside, resolvedRoot)
	if err != nil {
		t.Fatalf("映射目录内文件被错误拒绝: %v", err)
	}
	want, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("inside resolved=%q want %q", resolved, want)
	}
	if _, err := resolveLocalUploadSourceUnderRoot(link, resolvedRoot); err == nil {
		t.Fatal("指向映射目录外的链接未被拒绝")
	}
}

func TestSystemJunkFilter(t *testing.T) {
	junkFiles := []string{".DS_Store", ".localized", "Thumbs.db", "desktop.ini", "._photo.jpg", "._.DS_Store"}
	for _, f := range junkFiles {
		if !isSystemJunkFile(f) {
			t.Errorf("应判定为系统垃圾文件: %s", f)
		}
	}
	if isSystemJunkFile("movie.mkv") || isSystemJunkFile("poster.jpg") {
		t.Error("普通文件不应被判定为垃圾文件")
	}
	junkDirs := []string{"__MACOSX", ".Spotlight-V100", ".Trashes", ".fseventsd", "$RECYCLE.BIN", "System Volume Information", ".Trash-1000"}
	for _, d := range junkDirs {
		if !isSystemJunkDir(d) {
			t.Errorf("应判定为系统垃圾目录: %s", d)
		}
	}
	if isSystemJunkDir("电影") || isSystemJunkDir("Season 1") {
		t.Error("普通目录不应被判定为垃圾目录")
	}
}

func TestBuildLocalUploadSourcesForSelectedFolderKeepsOnlySelectedFolderAsRoot(t *testing.T) {
	base := t.TempDir()
	selectedDir := filepath.Join(base, "资料", "个人资料", "工作", "A项目")
	nestedDir := filepath.Join(selectedDir, "子目录")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(selectedDir, "根文件.txt")
	childFile := filepath.Join(nestedDir, "子文件.txt")
	if err := os.WriteFile(rootFile, []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFile, []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := buildLocalUploadSources(selectedDir, "资料/个人资料/工作/A项目", true)
	if err != nil {
		t.Fatalf("buildLocalUploadSources error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(sources))
	}

	got := map[string]string{}
	for _, item := range sources {
		got[filepath.Base(item.abs)] = item.relDir
	}
	if got["根文件.txt"] != "A项目" {
		t.Fatalf("root file relDir = %q, want %q", got["根文件.txt"], "A项目")
	}
	if got["子文件.txt"] != "A项目/子目录" {
		t.Fatalf("child file relDir = %q, want %q", got["子文件.txt"], "A项目/子目录")
	}
}

func TestBuildLocalUploadSourcesForSingleFileKeepsRootTarget(t *testing.T) {
	base := t.TempDir()
	filePath := filepath.Join(base, "单文件.txt")
	if err := os.WriteFile(filePath, []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := buildLocalUploadSources(filePath, "资料/个人资料/工作/单文件.txt", false)
	if err != nil {
		t.Fatalf("buildLocalUploadSources error = %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(sources))
	}
	if sources[0].relDir != "" {
		t.Fatalf("file relDir = %q, want empty", sources[0].relDir)
	}
	if sources[0].abs != filePath {
		t.Fatalf("file abs = %q, want %q", sources[0].abs, filePath)
	}
}

func TestTrimUploadBatchRoot(t *testing.T) {
	cases := map[string]string{
		"短剧/01.mp4":          "01.mp4",
		"短剧/Season 1/01.mp4": "Season 1/01.mp4",
		"短剧":                 "",
		"电影/01.mp4":          "电影/01.mp4",
	}
	for input, want := range cases {
		if got := trimUploadBatchRoot(input, "短剧"); got != want {
			t.Errorf("trimUploadBatchRoot(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsWithinRootBoundaries(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "data", "media")
	cases := []struct {
		name string
		abs  string
		want bool
	}{
		{"根目录自身", root, true},
		{"根内文件", filepath.Join(root, "a.mkv"), true},
		{"根内深层目录", filepath.Join(root, "s1", "e1.mkv"), true},
		{"兄弟目录仅前缀相同", filepath.Join(string(filepath.Separator), "data", "media2", "x.mkv"), false},
		{"父目录", filepath.Join(string(filepath.Separator), "data"), false},
		{"完全无关路径", filepath.Join(string(filepath.Separator), "etc", "passwd"), false},
	}
	for _, c := range cases {
		if got := isWithinRoot(c.abs, root); got != c.want {
			t.Errorf("%s: isWithinRoot(%q, %q) = %v, want %v", c.name, c.abs, root, got, c.want)
		}
	}
}

func TestLocalUploadReusesResolvedRootForEveryFile(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "mapping-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("创建根目录符号链接: %v", err)
	}

	inside := filepath.Join(realRoot, "影片.mkv")
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "外部.mkv")
	escapingLink := filepath.Join(realRoot, "逃逸.mkv")
	if err := os.WriteFile(inside, []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, escapingLink); err != nil {
		t.Fatalf("创建逃逸符号链接: %v", err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(linkRoot)
	if err != nil {
		t.Fatalf("解析映射根目录: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedRoot != wantRoot {
		t.Fatalf("resolvedRoot = %q, want %q", resolvedRoot, wantRoot)
	}

	wantInside, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		got, err := resolveLocalUploadSourceUnderRoot(inside, resolvedRoot)
		if err != nil {
			t.Fatalf("第 %d 次：根内文件被拒绝: %v", i, err)
		}
		if got != wantInside {
			t.Fatalf("第 %d 次：resolved = %q, want %q", i, got, wantInside)
		}
	}

	if _, err := resolveLocalUploadSourceUnderRoot(escapingLink, resolvedRoot); err == nil {
		t.Fatal("指向根目录外的符号链接必须被拒绝")
	}
	if _, err := resolveLocalUploadSourceUnderRoot(outside, resolvedRoot); err == nil {
		t.Fatal("根目录外的文件必须被拒绝")
	}
	if _, err := resolveLocalUploadSourceUnderRoot(filepath.Join(realRoot, "不存在.mkv"), resolvedRoot); err == nil {
		t.Fatal("不存在的文件应返回错误")
	}
}
