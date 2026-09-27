package mediaorganize

import "testing"

func TestSummaryCountsPendingAndUnidentifiedDiagnostics(t *testing.T) {
	plan := &Plan{Actions: []PlanAction{
		{Kind: ActionKindRelocate, SourceID: "pending", Status: "pending"},
		{Kind: ActionKindRelocate, SourceID: "rename", Status: "done", SourceParentID: "a", TargetParentID: "a"},
		{Kind: ActionKindRelocate, SourceID: "move", Status: "done", SourceParentID: "a", TargetParentID: "b"},
		{Kind: "mkdir", SourceID: "directory", Status: "done"},
	}, Skipped: []map[string]any{
		{"file_id": "pending", "reason": "未识别"},
		{"reason": "已整理"},
		{"reason": "未识别"},
	}}
	got := summarizePlan(plan, true)
	for key, want := range map[string]int{"total": 5, "renamed": 1, "moved": 1, "skipped": 2, "normal_skipped": 1, "abnormal_skipped": 1, "failed": 0, "pending": 1} {
		if got[key] != want {
			t.Fatalf("%s=%v，期望%d", key, got[key], want)
		}
	}
	// 口径自洽：总数 == 各分桶之和。
	sum := 0
	for _, key := range []string{"renamed", "moved", "skipped", "failed", "pending"} {
		sum += got[key].(int)
	}
	if got["total"] != sum {
		t.Fatalf("total=%v 与分桶之和 %d 不一致", got["total"], sum)
	}
	if got["stopped"] != true {
		t.Fatal("中止标记丢失")
	}
}

func TestSummaryDeduplicatesConflictDiagnostics(t *testing.T) {
	p := &Plan{Actions: []PlanAction{
		{Kind: ActionKindRelocate, SourceID: "1", Status: "skipped", Error: "目标已存在同名（未开启覆盖）"},
		{Kind: ActionKindRelocate, SourceID: "2", Status: "done"},
	}, Skipped: []map[string]any{
		{"file_id": "1", "reason": "目标已存在同名（未开启覆盖）"},
		{"file_id": "3", "reason": "已整理"},
		{"file_id": "3", "reason": "已整理"},
	}}
	got := summarizePlan(p, false)
	for k, want := range map[string]int{"total": 3, "skipped": 2, "normal_skipped": 2, "abnormal_skipped": 0, "failed": 0} {
		if got[k] != want {
			t.Fatalf("%s=%v，期望%d", k, got[k], want)
		}
	}
}

func TestSkipClassification(t *testing.T) {
	for _, tc := range []struct {
		reason string
		normal bool
	}{
		{"已整理", true}, {"已是目标名", true}, {"已并入「作品」", true},
		{"已合并到「作品」；源目录内文件将自动搬入该目录", true},
		{"已合并到「作品」（同一部作品，文件已自动并入，空目录将清理）", true},
		{"作品已在「作品」整理，文件已自动并入", true},
		{"无法识别", false}, {"未匹配 TMDB", false}, {"无法识别集数", false},
		{"目标已存在同名（未开启覆盖）", true}, {"执行期间目标已存在同名", true},
		{"另一项也将生成同名「已整理.mkv」", false},
	} {
		if got := isNormalSkip(tc.reason, ""); got != tc.normal {
			t.Errorf("%s=%v", tc.reason, got)
		}
	}
}

func TestSummaryPolicySkipsKeepRealConflictsAndFailures(t *testing.T) {
	plan := &Plan{Actions: []PlanAction{
		{Kind: ActionKindRelocate, SourceID: "1", Status: "skipped", Error: "目标已存在同名（未开启覆盖）"},
		{Kind: ActionKindRelocate, SourceID: "2", Status: "skipped", Error: "执行期间目标已存在同名"},
		{Kind: ActionKindRelocate, SourceID: "3", Status: "skipped", Error: "另一项也将生成同名「剧集.S01E01.mkv」"},
		{Kind: ActionKindRelocate, SourceID: "4", Status: "failed", Error: "覆盖冲突文件失败: 权限不足"},
	}}
	got := summarizePlan(plan, false)
	for key, want := range map[string]int{"total": 4, "skipped": 3, "normal_skipped": 2, "abnormal_skipped": 1, "failed": 1} {
		if got[key] != want {
			t.Fatalf("%s=%v，期望%d", key, got[key], want)
		}
	}
}

func TestSummaryNormalMergeAndFailedAction(t *testing.T) {
	p := &Plan{Actions: []PlanAction{{Kind: ActionKindRelocate, SourceID: "1", Status: "failed", Error: "移动失败"}}, Skipped: []map[string]any{
		{"file_id": "1", "reason": "已整理"},
		{"file_id": "2", "reason": "已合并到「作品」；源目录内文件将自动搬入该目录"},
	}}
	got := summarizePlan(p, false)
	if got["total"] != 2 || got["failed"] != 1 || got["normal_skipped"] != 1 || got["abnormal_skipped"] != 0 {
		t.Fatal(got)
	}
}
