package strm

import (
	"context"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
)

// 当前目录命中哪个任务、以及它相对任务根的目录段，必须按「目录段数组」算。
// 显示路径是用 "/" 拼起来的字符串，目录名自带斜杠时和三层目录长得一样，
// 按字符串相减会把一个目录名拆成多层，本地就会建出多层目录。
func TestMatchTaskForCurrentDirectoryUsesDirSegments(t *testing.T) {
	tasks := []*domain.StrmTask{
		{ID: 1, Path: "/库", OutputFolder: "任务"},
		{ID: 2, Path: "/库/电影", OutputFolder: "任务"},
	}

	cases := []struct {
		name    string
		dirs    []string
		wantID  int64
		wantRel []string
	}{
		{"普通目录", []string{"库", "剧集"}, 1, []string{"剧集"}},
		{"名字带斜杠的目录只算一段", []string{"库", "abc/def/ghi"}, 1, []string{"abc/def/ghi"}},
		{"名字带斜杠的目录再进一层", []string{"库", "abc/def/ghi", "Season 1"}, 1, []string{"abc/def/ghi", "Season 1"}},
		{"命中更深的任务", []string{"库", "电影", "2024"}, 2, []string{"2024"}},
		{"不在任何任务下", []string{"别的", "目录"}, 0, nil},
	}
	for _, c := range cases {
		task, rel := matchTaskForCurrentDirectory(tasks, "/"+joinForTest(c.dirs), c.dirs)
		if c.wantID == 0 {
			if task != nil {
				t.Fatalf("%s：不应命中任务，实际命中 %d（rel=%v）", c.name, task.ID, rel)
			}
			continue
		}
		if task == nil || task.ID != c.wantID {
			t.Fatalf("%s：命中任务=%v，期望 %d", c.name, task, c.wantID)
		}
		if len(rel) != len(c.wantRel) {
			t.Fatalf("%s：相对目录=%v，期望 %v", c.name, rel, c.wantRel)
		}
		for i := range rel {
			if rel[i] != c.wantRel[i] {
				t.Fatalf("%s：相对目录=%v，期望 %v", c.name, rel, c.wantRel)
			}
		}
	}
}

// 任务根自己就是「名字带斜杠的目录」时，相对目录必须为空（不能把任务根再算一遍）。
func TestMatchTaskForCurrentDirectoryTaskRootWithSlashName(t *testing.T) {
	tasks := []*domain.StrmTask{{ID: 9, Path: "/库/abc/def/ghi", OutputFolder: "任务"}}
	task, rel := matchTaskForCurrentDirectory(tasks, "/库/abc/def/ghi", []string{"库", "abc/def/ghi"})
	if task == nil || task.ID != 9 {
		t.Fatalf("应命中任务 9，实际 %v", task)
	}
	if len(rel) != 0 {
		t.Fatalf("任务根自身的相对目录应为空，实际 %v", rel)
	}
}

// 旧前端不带目录段数组时，必须退回原来的按显示路径相减，行为不变。
func TestMatchTaskForCurrentDirectoryFallsBackToDisplayPath(t *testing.T) {
	tasks := []*domain.StrmTask{{ID: 1, Path: "/库", OutputFolder: "任务"}}
	task, rel := matchTaskForCurrentDirectory(tasks, "/库/电影", nil)
	if task == nil || task.ID != 1 {
		t.Fatalf("应命中任务 1，实际 %v", task)
	}
	if len(rel) != 1 || rel[0] != "电影" {
		t.Fatalf("相对目录=%v，期望 [电影]", rel)
	}
}

// 端到端：目录名带斜杠时，按需生成的本地目录只能有一层。
func TestPrepareCurrentDirectoryWorkKeepsSingleLocalFolder(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	ctx := context.Background()
	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name: "测试账号", DriverType: "localfs", IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StrmTasks.Create(ctx, &domain.StrmTask{
		Name: "云影音", AccountID: accountID, ParentID: "library",
		Path: "/库", OutputFolder: "任务", Status: domain.StrmStatusActive,
	}); err != nil {
		t.Fatal(err)
	}

	work, err := svc.prepareCurrentDirectoryWork(ctx, accountID, "weird-id",
		"/库/abc/def/ghi", []string{"库", "abc/def/ghi"}, nil)
	if err != nil {
		t.Fatalf("准备工作失败: %v", err)
	}
	if work == nil {
		t.Fatal("应命中任务")
	}
	if len(work.relDirs) != 1 || work.relDirs[0] != "abc/def/ghi" {
		t.Fatalf("相对目录=%v，期望 [abc/def/ghi]", work.relDirs)
	}
	got := filepath.ToSlash(localTaskDir(work.root, work.outputFolder, work.relDirs))
	if filepath.Base(got) != "abc_def_ghi" {
		t.Fatalf("本地目录=%q，期望最后一层是 abc_def_ghi", got)
	}
}

func joinForTest(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}
