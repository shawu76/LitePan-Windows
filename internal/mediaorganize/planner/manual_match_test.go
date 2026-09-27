package planner_test

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/planner"
)

func TestReplanMatchedGroupRebuildsTVWorkDir(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "show1", Name: "转生贵族的异世界冒险录", IsDir: true},
		},
		"show1": {
			{ID: "f1", Name: "转生贵族的异世界冒险录.S01E01.1080p.mkv"},
		},
	}}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			TargetRootID:      "/已整理",
			ActionType:        "move",
			MediaType:         "tv",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{
			"mo_tmdb_api_key": "test-key",
		},
		"task-test",
		nil,
		func(string) {},
		nil,
		func() error { return nil },
	)

	plan, err := p.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID:  "tv|show1|转生贵族的异世界冒险录|转生贵族的异世界冒险录",
		MediaKind: "tv",
		DirID:     "show1",
		DirName:   "转生贵族的异世界冒险录",
		Title:     "转生贵族的异世界冒险录",
	}, map[string]any{
		"id":             220999,
		"name":           "转生贵族的异世界冒险录",
		"first_air_date": "2023-01-01",
		"media_type":     "tv",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Actions) < 3 {
		t.Fatalf("手动匹配重建动作过少: %+v", plan.Actions)
	}

	workIdx, seasonIdx, fileIdx := -1, -1, -1
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch {
		case action.Kind == "ensure_dir" && action.Metadata["is_work_dir"] == true:
			workIdx = i
		case action.Kind == "ensure_dir" && action.Metadata["is_season_dir"] == true:
			seasonIdx = i
		case action.SourceID == "f1":
			fileIdx = i
		}
	}
	if workIdx < 0 || seasonIdx < 0 || fileIdx < 0 {
		t.Fatalf("未生成预期动作: %+v", plan.Actions)
	}
	if got := plan.Actions[workIdx].TargetName; got != "转生贵族的异世界冒险录 (2023) {tmdb-220999}" {
		t.Fatalf("作品目录名错误: %q", got)
	}
	if got, want := plan.Actions[seasonIdx].TargetParentID, "ref:"+plan.Actions[workIdx].ID; got != want {
		t.Fatalf("季目录动作错误: %+v", plan.Actions[seasonIdx])
	}
	if got, want := plan.Actions[fileIdx].TargetParentID, "ref:"+plan.Actions[seasonIdx].ID; got != want {
		t.Fatalf("文件动作未指向季目录: %+v", plan.Actions[fileIdx])
	}
	if !strings.Contains(plan.Actions[fileIdx].TargetName, "转生贵族的异世界冒险录 (2023) S01E01") {
		t.Fatalf("文件名未使用手动匹配后的标准名: %q", plan.Actions[fileIdx].TargetName)
	}
	var cleanup *moplan.PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].Kind == moplan.ActionKindDeleteEmptyDir && plan.Actions[i].SourceID == "show1" {
			cleanup = &plan.Actions[i]
			break
		}
	}
	if cleanup == nil {
		t.Fatalf("手动匹配后应清理搬空的源目录: %+v", plan.Actions)
	}
	if len(cleanup.DependsOn) != 1 || cleanup.DependsOn[0] != plan.Actions[fileIdx].ID {
		t.Fatalf("空目录清理应等待文件移动完成: %+v", cleanup)
	}
}

func TestReplanMatchedGroupUsesSelectedTVTypeForBareNumberedFiles(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show1", Name: "藏锋 (2026)", IsDir: true}},
		"show1": {
			{ID: "f1", Name: "01.mp4"},
			{ID: "f2", Name: "02.mp4"},
		},
	}}
	p := planner.New(
		context.Background(), fs, 1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			TargetRootID:      "/已整理",
			ActionType:        "move",
			MediaType:         "auto",
			RenameMarker:      "tmdb",
			UseTMDB:           true,
			Recursive:         true,
		},
		planner.Settings{"mo_tmdb_api_key": "test-key"},
		"task-test", nil, func(string) {}, nil, func() error { return nil },
	)

	plan, err := p.ReplanMatchedGroup(planner.ManualMatchGroup{
		GroupUID:  "movie|show1|藏锋 (2026)|藏锋",
		MediaKind: "tv",
		DirID:     "show1",
		DirName:   "藏锋 (2026)",
		Title:     "藏锋",
	}, map[string]any{
		"id":             280133,
		"name":           "藏锋",
		"first_air_date": "2026-01-01",
		"media_type":     "tv",
	})
	if err != nil {
		t.Fatal(err)
	}

	var workDir, seasonDir, firstEpisode *moplan.PlanAction
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch {
		case action.Kind == "ensure_dir" && action.Metadata["is_work_dir"] == true:
			workDir = action
		case action.Kind == "ensure_dir" && action.Metadata["is_season_dir"] == true:
			seasonDir = action
		case action.SourceID == "f1":
			firstEpisode = action
		}
	}
	if workDir == nil || workDir.TargetName != "藏锋 (2026) {tmdb-280133}" {
		t.Fatalf("手动选中电视剧后作品目录错误: %+v", workDir)
	}
	if seasonDir == nil || seasonDir.TargetName != "Season 01" {
		t.Fatalf("手动选中电视剧后应生成季目录: %+v", seasonDir)
	}
	if firstEpisode == nil || firstEpisode.Metadata["media_kind"] != "tv" || !strings.Contains(firstEpisode.TargetName, "S01E01") {
		t.Fatalf("纯数字文件应按用户选中的电视剧重排: %+v", firstEpisode)
	}
}

// TestNeedsMatchDetected 验证识别不到的作品会进入 needs_match，供用户手动匹配。
func TestNeedsMatchDetected(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "d1", Name: "Unknowable 2020", IsDir: true},
		},
		"d1": {
			{ID: "f1", Name: "Unknowable.2020.1080p.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		lookupFn: func(id string) map[string]any {
			if id == "603" {
				return map[string]any{
					"id": 603, "title": "The Matrix", "original_title": "The Matrix",
					"year": 1999, "release_date": "1999-03-31", "media_type": "movie",
				}
			}
			return nil
		},
	}

	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	needs, _ := plan.Diagnostics["needs_match"].([]map[string]any)
	if len(needs) == 0 {
		t.Fatalf("计划应产生 needs_match，diagnostics=%v", plan.Diagnostics)
	}
}

// TestSpecialDirEpisodeGoesToSeason00 复现用户结构：番外目录里 S00E01 剧集文件应归入
// 剧集 Season 00，同一目录里的纯电影文件仍按独立电影处理。
func TestSpecialDirEpisodeGoesToSeason00(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "一人之下", IsDir: true}},
		"show": {{ID: "cat", Name: "前五季+番外+剧场版", IsDir: true}},
		"cat":  {{ID: "sp", Name: "番外篇 天师下山（2018）", IsDir: true}},
		"sp": {
			{ID: "f1", Name: "一人之下.S00E01.2018.1080P.WEB-DL.AAC.mp4"},
			{ID: "f2", Name: "天师下山.2018.1080p.mkv"},
			{ID: "f3", Name: "一人之下 番外篇 天师下山.1080p.mkv"},
		},
	}}
	tmdb := &mockTMDB{
		searchFn: func(query string, _ *int) []map[string]any {
			switch {
			case strings.Contains(query, "一人之下"):
				return []map[string]any{{"id": 800, "name": "一人之下", "first_air_date": "2016-07-08"}}
			case strings.Contains(query, "天师"):
				return []map[string]any{{"id": 900, "title": "天师下山", "release_date": "2018-01-01"}}
			}
			return nil
		},
	}
	plan, err := newTestPlanner(fs, tmdb, "root").Build()
	if err != nil {
		t.Fatal(err)
	}
	var f1, f2, f3 *moplan.PlanAction
	for i := range plan.Actions {
		a := &plan.Actions[i]
		switch a.SourceID {
		case "f1":
			f1 = a
		case "f2":
			f2 = a
		case "f3":
			f3 = a
		}
	}
	if f1 == nil {
		t.Fatalf("f1 未生成动作: %+v", plan.Actions)
	}
	if f1.Metadata["media_kind"] != "tv" || !strings.Contains(f1.TargetName, "S00E01") {
		t.Fatalf("S00E01 文件应归入剧集 Season 00: media_kind=%v target=%q", f1.Metadata["media_kind"], f1.TargetName)
	}
	if f2 == nil {
		t.Fatalf("f2 未生成动作: %+v", plan.Actions)
	}
	if f2.Metadata["media_kind"] != "movie" {
		t.Fatalf("同目录纯电影文件应判独立电影: media_kind=%v", f2.Metadata["media_kind"])
	}
	if f3 == nil {
		t.Fatalf("f3 未生成动作: %+v", plan.Actions)
	}
	if f3.Metadata["media_kind"] != "tv" {
		t.Fatalf("文件名含剧集名的番外应归剧集: media_kind=%v target=%q", f3.Metadata["media_kind"], f3.TargetName)
	}
}
