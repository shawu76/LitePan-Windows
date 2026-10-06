package mediaorganize

import (
	"context"
	"slices"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/classification"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

type bindingClassificationStub struct{}

func (bindingClassificationStub) Available() bool                 { return true }
func (bindingClassificationStub) RootDirectories(string) []string { return []string{"电视剧"} }
func (bindingClassificationStub) Classify(context.Context, classification.Request) (classification.Decision, error) {
	return classification.Decision{
		Applied: true, Matched: true, Template: "media_type", Category: "电视剧",
		RelativeSegments: []string{"电视剧"},
	}, nil
}

type bindingFS struct{ dirs map[string][]domain.FileItem }

func (f bindingFS) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), f.dirs[parentID]...), nil
}

func TestBindingReplacePlanGroupRebuildsSingleGroup(t *testing.T) {
	uid := "tv|show1|旧目录|旧标题"
	plan := &Plan{
		TaskID: "task-1",
		Actions: []moplan.PlanAction{
			{
				ID:         "a1",
				Kind:       moplan.ActionKindRelocate,
				SourceID:   "other-file",
				TargetName: "Other.mkv",
				Metadata: map[string]any{
					"group_uid": "movie|other|Other|Other",
				},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "/已整理",
				TargetName:     "旧作品目录",
				Metadata: map[string]any{
					"is_work_dir":   true,
					"source_dir_id": "show1",
				},
			},
			{
				ID:             "a3",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "ref:a2",
				TargetName:     "Season 01",
				DependsOn:      []string{"a2"},
				Metadata: map[string]any{
					"is_season_dir": true,
				},
			},
			{
				ID:             "a4",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "old-file.mkv",
				SourceParentID: "show1",
				TargetParentID: "ref:a3",
				TargetName:     "旧标题 S01E01.mkv",
				DependsOn:      []string{"a2", "a3"},
				Metadata: map[string]any{
					"group_uid":  uid,
					"media_kind": "tv",
					"title":      "旧标题",
				},
			},
		},
		Skipped: []map[string]any{
			{"file_id": "f1", "file_name": "old-file.mkv", "reason": "旧跳过"},
			{"file_id": "other-file", "file_name": "Other.mkv", "reason": "保留"},
		},
		Diagnostics: map[string]any{
			"needs_match": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "旧标题", "media_kind": "tv"},
				{"group_uid": "movie|other|Other|Other", "title": "Other"},
			},
			"groups": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "旧标题", "media_kind": "tv"},
				{"group_uid": "movie|other|Other|Other", "title": "Other"},
			},
			"meta_followers": []map[string]any{
				{"file_id": "f1", "depend_on": "a4", "new_base": "旧标题 S01E01"},
				{"file_id": "other-file", "depend_on": "a1", "new_base": "Other"},
			},
		},
	}
	rebuilt := &Plan{
		TaskID: "task-1",
		Actions: []moplan.PlanAction{
			{
				ID:             "a1",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "/已整理",
				TargetName:     "新作品目录 (2023) {tmdb-220999}",
				Metadata: map[string]any{
					"is_work_dir":   true,
					"source_dir_id": "show1",
				},
			},
			{
				ID:             "a2",
				Kind:           moplan.ActionKindEnsureDir,
				TargetParentID: "ref:a1",
				TargetName:     "Season 01",
				DependsOn:      []string{"a1"},
				Metadata: map[string]any{
					"is_season_dir": true,
				},
			},
			{
				ID:             "a3",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "f1",
				SourceName:     "old-file.mkv",
				SourceParentID: "show1",
				TargetParentID: "ref:a2",
				TargetName:     "新作品目录 (2023) {tmdb-220999} S01E01.mkv",
				DependsOn:      []string{"a1", "a2"},
				Metadata: map[string]any{
					"group_uid":  uid,
					"media_kind": "tv",
					"title":      "新标题",
					"tmdb_id":    "220999",
				},
			},
		},
		Diagnostics: map[string]any{
			"groups": []map[string]any{
				{"group_uid": uid, "dir_id": "show1", "dir_name": "旧目录", "title": "新标题", "media_kind": "tv"},
			},
			"meta_followers": []map[string]any{
				{"file_id": "f1", "depend_on": "a3", "new_base": "新标题 S01E01"},
			},
		},
	}

	got := bindingReplacePlanGroup(plan, uid, rebuilt)
	if len(got.Actions) != 4 {
		t.Fatalf("动作数不对: %d", len(got.Actions))
	}
	if got.Actions[0].SourceID != "other-file" {
		t.Fatalf("其他组动作不应被删除: %+v", got.Actions[0])
	}

	workDir := got.Actions[1]
	if workDir.ID != "a2" || workDir.TargetName != "新作品目录 (2023) {tmdb-220999}" {
		t.Fatalf("作品目录动作未正确替换: %+v", workDir)
	}
	seasonDir := got.Actions[2]
	if seasonDir.ID != "a3" || seasonDir.TargetParentID != "ref:a2" {
		t.Fatalf("季目录动作引用未重写: %+v", seasonDir)
	}
	fileAction := got.Actions[3]
	if fileAction.ID != "a4" || fileAction.TargetParentID != "ref:a3" {
		t.Fatalf("文件动作引用未重写: %+v", fileAction)
	}
	if len(fileAction.DependsOn) != 2 || fileAction.DependsOn[0] != "a2" || fileAction.DependsOn[1] != "a3" {
		t.Fatalf("文件动作 DependsOn 未重写: %+v", fileAction.DependsOn)
	}

	if len(got.Skipped) != 1 || got.Skipped[0]["file_id"] != "other-file" {
		t.Fatalf("旧组 skipped 未被清理: %+v", got.Skipped)
	}

	needs := bindingMapSlice(got.Diagnostics["needs_match"])
	if len(needs) != 1 || needs[0]["group_uid"] != "movie|other|Other|Other" {
		t.Fatalf("needs_match 未正确替换: %+v", needs)
	}
	groups := bindingMapSlice(got.Diagnostics["groups"])
	if len(groups) != 2 {
		t.Fatalf("groups 数量不对: %+v", groups)
	}
	var matchedGroup map[string]any
	for _, entry := range groups {
		if entry["group_uid"] == uid {
			matchedGroup = entry
			break
		}
	}
	if matchedGroup == nil || matchedGroup["title"] != "新标题" {
		t.Fatalf("groups 未替换成新组信息: %+v", groups)
	}

	followers := bindingMapSlice(got.Diagnostics["meta_followers"])
	if len(followers) != 2 {
		t.Fatalf("meta_followers 数量不对: %+v", followers)
	}
	for _, entry := range followers {
		if entry["file_id"] == "f1" && entry["depend_on"] != "a4" {
			t.Fatalf("meta_followers depend_on 未重写: %+v", entry)
		}
	}
}

func TestBindingFindManualMatchGroupPrefersNeedsMatch(t *testing.T) {
	uid := "movie|d1|Unknowable 2020|Unknowable"
	plan := &Plan{
		Diagnostics: map[string]any{
			"needs_match": []any{
				map[string]any{
					"group_uid":  uid,
					"media_kind": "movie",
					"dir_id":     "d1",
					"dir_name":   "Unknowable 2020",
					"title":      "Unknowable",
				},
			},
		},
	}

	group := bindingFindManualMatchGroup(plan, uid, "")
	if group.GroupUID != uid || group.MediaKind != "movie" || group.DirID != "d1" || group.DirName != "Unknowable 2020" || group.Title != "Unknowable" {
		t.Fatalf("手动匹配组信息提取错误: %+v", group)
	}
}

func TestBindingReplacePlanGroupWithoutOldActionsClearsOldSkipped(t *testing.T) {
	uid := "movie|d1|Unknowable 2020|Unknowable"
	plan := &Plan{
		Skipped: []map[string]any{
			{"file_id": "f1", "file_name": "Unknowable.2020.mkv", "reason": "无法识别"},
			{"file_id": "other", "file_name": "Other.mkv", "reason": "保留"},
		},
		Diagnostics: map[string]any{
			"needs_match": []map[string]any{
				{"group_uid": uid, "media_kind": "movie", "dir_id": "d1", "dir_name": "Unknowable 2020", "title": "Unknowable"},
			},
		},
	}
	rebuilt := &Plan{
		Actions: []moplan.PlanAction{
			{
				ID:       "a1",
				Kind:     moplan.ActionKindRelocate,
				SourceID: "f1",
				Metadata: map[string]any{"group_uid": uid, "media_kind": "movie"},
			},
		},
		Diagnostics: map[string]any{
			"groups": []map[string]any{
				{"group_uid": uid, "media_kind": "movie", "dir_id": "d1", "title": "Matched"},
			},
		},
	}

	got := bindingReplacePlanGroup(plan, uid, rebuilt)
	if len(got.Actions) != 1 || got.Actions[0].SourceID != "f1" {
		t.Fatalf("无旧动作时未正确加入重建动作: %+v", got.Actions)
	}
	if len(got.Skipped) != 1 || got.Skipped[0]["file_id"] != "other" {
		t.Fatalf("无旧动作时未清理该组旧 skipped: %+v", got.Skipped)
	}
	if needs := bindingMapSlice(got.Diagnostics["needs_match"]); len(needs) != 0 {
		t.Fatalf("旧 needs_match 未清理: %+v", needs)
	}
	groups := bindingMapSlice(got.Diagnostics["groups"])
	if len(groups) != 1 || groups[0]["title"] != "Matched" {
		t.Fatalf("重建后的 groups 不正确: %+v", groups)
	}
}

func TestBindingReplacePlanGroupMergesRenameTVSeasonsIntoExistingWorkDir(t *testing.T) {
	oldUID := "tv|show2025|我叫赵甲第 (2025)|我叫赵甲第2"
	plan := &Plan{
		Actions: []moplan.PlanAction{
			{
				ID: "a1", Kind: moplan.ActionKindRelocate,
				SourceID: "show2022", SourceName: "我叫赵甲第 (2022)", SourceParentID: "root",
				TargetParentID: "root", TargetName: "我叫赵甲第 (2022) {tmdb-196615}",
				Metadata: map[string]any{"kind_label": "dir_rename", "group_uid": "tv|show2022|我叫赵甲第 (2022)|我叫赵甲第"},
			},
			{
				ID: "a2", Kind: moplan.ActionKindEnsureDir,
				TargetParentID: "show2022", TargetName: "Season 01", DependsOn: []string{"a1"},
			},
		},
		Diagnostics: map[string]any{
			"needs_match": []map[string]any{{"group_uid": oldUID}},
		},
	}
	rebuilt := &Plan{
		Actions: []moplan.PlanAction{
			{
				ID: "a1", Kind: moplan.ActionKindRelocate,
				SourceID: "show2025", SourceName: "我叫赵甲第 (2025)", SourceParentID: "root",
				TargetParentID: "root", TargetName: "我叫赵甲第 (2022) {tmdb-196615}",
				Metadata: map[string]any{"kind_label": "dir_rename", "group_uid": oldUID},
			},
			{
				ID: "a2", Kind: moplan.ActionKindEnsureDir,
				TargetParentID: "show2025", TargetName: "Season 02", DependsOn: []string{"a1"},
			},
			{
				ID: "a3", Kind: moplan.ActionKindRelocate,
				SourceID: "s02e01", SourceParentID: "show2025", TargetParentID: "ref:a2",
				TargetName: "我叫赵甲第 (2022) S02E01.mkv", DependsOn: []string{"a1", "a2"},
				Metadata: map[string]any{"group_uid": oldUID, "media_kind": "tv"},
			},
		},
		Diagnostics: map[string]any{},
	}

	got := bindingReplacePlanGroup(plan, oldUID, rebuilt)
	var losingRename, season2, episode, cleanup *moplan.PlanAction
	for i := range got.Actions {
		action := &got.Actions[i]
		switch {
		case action.Kind == moplan.ActionKindRelocate && action.SourceID == "show2025":
			losingRename = action
		case action.TargetName == "Season 02":
			season2 = action
		case action.SourceID == "s02e01":
			episode = action
		case action.Kind == moplan.ActionKindDeleteEmptyDir && action.SourceID == "show2025":
			cleanup = action
		}
	}
	if losingRename == nil || losingRename.Status != "skipped" {
		t.Fatalf("第二个同作品目录改名应转为归并: %+v", losingRename)
	}
	if season2 == nil || season2.TargetParentID != "show2022" || !slices.Contains(season2.DependsOn, "a1") {
		t.Fatalf("Season 02 应创建在已匹配的作品目录中: %+v", season2)
	}
	if episode == nil || !strings.HasPrefix(episode.TargetParentID, "ref:") {
		t.Fatalf("第二季文件应放入 Season 02: %+v", episode)
	}
	if cleanup == nil || !slices.Contains(cleanup.DependsOn, episode.ID) {
		t.Fatalf("归并后应在文件迁移完成后清理旧目录: %+v", cleanup)
	}
}

func TestManualMatchPlannerKeepsClassificationForMove(t *testing.T) {
	fs := bindingFS{dirs: map[string][]domain.FileItem{
		"root":     {{ID: "show2025", Name: "我叫赵甲第 (2025)", IsDir: true}},
		"show2025": {{ID: "s02e01", Name: "我叫赵甲第2.2025.S02E01.mkv"}},
	}}
	p := planner.New(
		context.Background(), fs, 1,
		planner.TaskConfig{
			TargetDirectoryID: "root", TargetRootID: "新目录", ActionType: "move",
			MediaType: "auto", RenameMarker: "tmdb", UseTMDB: true, Recursive: true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test", nil, func(string) {}, nil, func() error { return nil },
	)
	svc := &Service{classification: bindingClassificationStub{}}
	svc.configureManualMatchPlanner(p)

	plan, err := p.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID: "tv|show2025|我叫赵甲第 (2025)|我叫赵甲第2", MediaKind: "tv",
		DirID: "show2025", DirName: "我叫赵甲第 (2025)", Title: "我叫赵甲第2", SourceIDs: []string{"s02e01"},
	}, map[string]any{
		"id": 196615, "name": "我叫赵甲第", "first_air_date": "2022-03-31", "media_type": "tv",
	})
	if err != nil {
		t.Fatal(err)
	}
	var category, work *moplan.PlanAction
	for i := range plan.Actions {
		action := &plan.Actions[i]
		if action.Kind == moplan.ActionKindEnsureDir && action.TargetName == "电视剧" {
			category = action
		}
		if action.Kind == moplan.ActionKindEnsureDir && action.Metadata["is_work_dir"] == true {
			work = action
		}
	}
	if category == nil {
		t.Fatalf("人工匹配局部重建应保留分类目录: %+v", plan.Actions)
	}
	if work == nil || work.TargetParentID != "ref:"+category.ID {
		t.Fatalf("作品目录应落在电视剧分类下: category=%+v work=%+v", category, work)
	}
}
