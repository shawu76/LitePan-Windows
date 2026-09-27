package planner_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/planner"
	"litepan/internal/mediaorganize/recognition"
)

type recognitionStub struct {
	calls         int
	req           recognition.BatchRequest
	err           error
	result        func(recognition.BatchRequest) recognition.BatchResult
	episodeCalls  int
	episodeReq    recognition.EpisodeRequest
	episodeReqs   []recognition.EpisodeRequest
	episodeResult func(recognition.EpisodeRequest) []recognition.FileResult
}

func (*recognitionStub) Available() bool { return true }

func (s *recognitionStub) Enhance(_ context.Context, req recognition.BatchRequest) (recognition.BatchResult, error) {
	s.calls++
	s.req = req
	if s.err != nil {
		return recognition.BatchResult{}, s.err
	}
	if s.result != nil {
		return s.result(req), nil
	}
	year := 2009
	items := make([]recognition.WorkResult, 0, len(req.Works))
	for i, work := range req.Works {
		title := "Avatar"
		if i > 0 {
			title = "Up"
		}
		items = append(items, recognition.WorkResult{
			WorkID:     work.WorkID,
			Recognized: true,
			Title:      title,
			Year:       &year,
			MediaType:  "movie",
		})
	}
	return recognition.BatchResult{Items: items}, nil
}

func (s *recognitionStub) ResolveEpisodes(_ context.Context, req recognition.EpisodeRequest) ([]recognition.FileResult, error) {
	s.episodeCalls++
	s.episodeReq = req
	s.episodeReqs = append(s.episodeReqs, req)
	if s.episodeResult != nil {
		return s.episodeResult(req), nil
	}
	return nil, nil
}

func TestPlannerUsesAIWhenPollutedTitlesMissTMDB(t *testing.T) {
	movieDir := "🔥流｜浪🌍地｜球｜2【㊙️完整版㊙️】〔2023〕‑4K‑HDR‑WEB‑DL【👉看更多高清ｖｏｄ‑ｆｉｌｍ８８．ｃｃ👉】🔥合集🔥"
	movieFile := "流✨浪｜地｜球｜2‑(2023)‑〔2160P HDR〕‑DDP5.1‑【点进网站看全集👉ｆｉｌｍ‑ｖｉｐ９９９．ｔｏｐ】‑国语多字幕‑修复版.mkv"
	tvDir := "🚔狂🔥飙【㊙️全集更新完毕㊙️】〖2023〗‑1080P‑WEB‑DL‑合集🔥【ｖｏｄ‑ｓｅｒｉｅｓ８８．ｔｏｐ】"
	tvFile := "狂｜飙‑S01E12‑(2023)‑〖1080P〗‑AAC‑【💢看全集👉ｓｅｒ‑ｖｉｐ‑ｐｌａｙ．ｃｃ💢】‑内嵌简中字幕.mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "movie", Name: movieDir, IsDir: true},
			{ID: "tv", Name: tvDir, IsDir: true},
		},
		"movie": {{ID: "movie-file", Name: movieFile, Size: 1024}},
		"tv":    {{ID: "tv-file", Name: tvFile, Size: 2048}},
	}}
	tmdb := &mockTMDB{searchFn: func(query string, _ *int) []map[string]any {
		switch strings.TrimSpace(query) {
		case "流浪地球2":
			return []map[string]any{{"id": 842675, "title": "流浪地球2", "original_title": "The Wandering Earth II", "release_date": "2023-01-22"}}
		case "狂飙":
			return []map[string]any{{"id": 215103, "name": "狂飙", "original_name": "The Knockout", "first_air_date": "2023-01-14"}}
		default:
			return nil
		}
	}}
	year := 2023
	season := 1
	episode := 12
	enhancer := &recognitionStub{result: func(req recognition.BatchRequest) recognition.BatchResult {
		items := make([]recognition.WorkResult, 0, len(req.Works))
		for _, work := range req.Works {
			if strings.Contains(work.Directory, "狂") {
				files := make([]recognition.FileResult, 0, len(work.Files))
				for _, file := range work.Files {
					files = append(files, recognition.FileResult{SourceID: file.SourceID, Episode: &episode, Kind: "episode"})
				}
				items = append(items, recognition.WorkResult{WorkID: work.WorkID, Recognized: true, Title: "狂飙", Year: &year, MediaType: "tv", Season: &season, Files: files})
				continue
			}
			items = append(items, recognition.WorkResult{WorkID: work.WorkID, Recognized: true, Title: "流浪地球2", Year: &year, MediaType: "movie"})
		}
		return recognition.BatchResult{Items: items}
	}}
	p := newTestPlanner(fs, tmdb, "root")
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 1 || len(enhancer.req.Works) != 2 {
		t.Fatalf("TMDB 未命中作品应批量交给 AI: calls=%d works=%d", enhancer.calls, len(enhancer.req.Works))
	}
	want := map[string]bool{"842675": false, "215103": false}
	for _, action := range plan.Actions {
		id, _ := action.Metadata["tmdb_id"].(string)
		id = strings.TrimSpace(id)
		if _, ok := want[id]; ok && action.Metadata["recognition_source"] == "ai" {
			want[id] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("AI 清理标题后未命中 TMDB %s: actions=%+v skipped=%+v", id, plan.Actions, plan.Skipped)
		}
	}
}

func TestPlannerKeepsBuiltInEpisodesAfterAIIdentifiesLongSeries(t *testing.T) {
	files := make([]domain.FileItem, 0, 100)
	for episode := 1; episode <= 100; episode++ {
		files = append(files, domain.FileItem{
			ID:   fmt.Sprintf("ep%03d", episode),
			Name: fmt.Sprintf("%03d.mp4", episode),
			Size: 1024,
		})
	}
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "藏丨锋乱码合集zzzz", IsDir: true}},
		"show": files,
	}}
	year := 2026
	season := 1
	enhancer := &recognitionStub{result: func(req recognition.BatchRequest) recognition.BatchResult {
		return recognition.BatchResult{Items: []recognition.WorkResult{{
			WorkID: req.Works[0].WorkID, Recognized: true, Title: "藏锋", Year: &year, MediaType: "tv", Season: &season,
		}}}
	}}
	tmdb := &mockTMDB{searchFn: func(query string, _ *int) []map[string]any {
		if strings.TrimSpace(query) == "藏锋" {
			return []map[string]any{{"id": 280133, "name": "藏锋", "first_air_date": "2026-01-01"}}
		}
		return nil
	}}
	p := newTestPlanner(fs, tmdb, "root")
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 1 {
		t.Fatalf("复杂作品名应只进行一次作品识别: calls=%d", enhancer.calls)
	}
	if enhancer.episodeCalls != 0 {
		t.Fatalf("内置规则已识别的 100 集不应再发给 AI: episode_calls=%d request=%+v", enhancer.episodeCalls, enhancer.episodeReq)
	}
	foundEpisode100 := false
	for _, action := range plan.Actions {
		if action.SourceID == "ep100" && strings.Contains(action.TargetName, "S01E100") {
			foundEpisode100 = true
			break
		}
	}
	if !foundEpisode100 {
		t.Fatalf("AI 识别作品后应继续使用内置集数整理: actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
	}
}

func TestPlannerOnlyAsksAIForUnparsedEpisodes(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "show", Name: "混乱剧名合集", IsDir: true}},
		"show": {
			{ID: "ep01", Name: "show.S01E01.mkv", Size: 1024},
			{ID: "ep02", Name: "show.final-part.mkv", Size: 1024},
		},
	}}
	year := 2026
	season := 1
	episode2 := 2
	enhancer := &recognitionStub{
		result: func(req recognition.BatchRequest) recognition.BatchResult {
			items := make([]recognition.WorkResult, 0, len(req.Works))
			for _, work := range req.Works {
				items = append(items, recognition.WorkResult{
					WorkID: work.WorkID, Recognized: true, Title: "测试剧", Year: &year, MediaType: "tv", Season: &season,
				})
			}
			return recognition.BatchResult{Items: items}
		},
		episodeResult: func(req recognition.EpisodeRequest) []recognition.FileResult {
			return []recognition.FileResult{{SourceID: req.Files[0].SourceID, Episode: &episode2}}
		},
	}
	tmdb := &mockTMDB{searchFn: func(query string, _ *int) []map[string]any {
		if strings.TrimSpace(query) == "测试剧" {
			return []map[string]any{{"id": 123456, "name": "测试剧", "first_air_date": "2026-01-01"}}
		}
		return nil
	}}
	p := newTestPlanner(fs, tmdb, "root")
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	requestedNames := make([]string, 0)
	for _, req := range enhancer.episodeReqs {
		for _, file := range req.Files {
			requestedNames = append(requestedNames, file.Name)
		}
	}
	if enhancer.episodeCalls == 0 || len(requestedNames) != 1 || requestedNames[0] != "show.final-part.mkv" {
		t.Fatalf("应只将内置规则无法解析的文件交给 AI: calls=%d files=%v", enhancer.episodeCalls, requestedNames)
	}
	foundEpisode2 := false
	for _, action := range plan.Actions {
		if action.SourceID == "ep02" && strings.Contains(action.TargetName, "S01E02") {
			foundEpisode2 = true
			break
		}
	}
	if !foundEpisode2 {
		t.Fatalf("AI 补判的集数未回到原有整理规则: actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
	}
}

func TestPlannerRenamesSingleTVRootWhenTMDBMatchesWithoutAI(t *testing.T) {
	tvDir := "🚔狂🔥飙【㊙️全集更新完毕㊙️】〖2023〗‑1080P‑WEB‑DL‑合集🔥【ｖｏｄ‑ｓｅｒｉｅｓ８８．ｔｏｐ】"
	tvFile := "狂｜飙‑S01E12‑(2023)‑〖1080P〗‑AAC‑【💢看全集👉ｓｅｒ‑ｖｉｐ‑ｐｌａｙ．ｃｃ💢】‑内嵌简中字幕.mkv"
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "tv", Name: tvDir, IsDir: true}},
		"tv":   {{ID: "episode", Name: tvFile, Size: 2048}},
	}}
	tmdb := &mockTMDB{searchFn: func(query string, _ *int) []map[string]any {
		if strings.Contains(query, "狂") {
			return []map[string]any{{"id": 210757, "name": "狂飙", "first_air_date": "2023-01-14"}}
		}
		return nil
	}}
	enhancer := &recognitionStub{}
	p := newTestPlanner(fs, tmdb, "root")
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 0 {
		t.Fatalf("TMDB 已直接命中时不应调用 AI: calls=%d", enhancer.calls)
	}
	for _, action := range plan.Actions {
		if action.SourceID == "tv" && action.TargetName == "狂飙 (2023) {tmdb-210757}" {
			return
		}
	}
	t.Fatalf("应将单作品根目录标准化为带 TMDB ID 的目录名: actions=%+v", plan.Actions)
}

func TestPlannerBatchesLowConfidenceGroupsIntoAI(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {
			{ID: "f1", Name: "ABC.mkv", Size: 1024},
			{ID: "f2", Name: "XYZ.mkv", Size: 2048},
		},
	}}
	enhancer := &recognitionStub{}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{
			TargetDirectoryID: "root",
			ActionType:        "rename",
			MediaType:         "movie",
			RenameMarker:      "off",
			UseTMDB:           false,
			Recursive:         true,
		},
		planner.Settings{},
		"task-ai",
		nil,
		func(string) {},
		nil,
		func() error { return nil },
	)
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 1 || len(enhancer.req.Works) != 2 {
		t.Fatalf("AI 应在扫描后批量调用一次: calls=%d request=%+v", enhancer.calls, enhancer.req)
	}
	if len(plan.Actions) == 0 || len(plan.Skipped) != 0 {
		t.Fatalf("AI 识别后应回到原计划器: actions=%+v skipped=%+v", plan.Actions, plan.Skipped)
	}
	if plan.Actions[0].Metadata["recognition_source"] != "ai" {
		t.Fatalf("计划未标记 AI 识别来源: %+v", plan.Actions[0].Metadata)
	}
}

func TestPlannerFallsBackWhenAIFails(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "f1", Name: "ABC.mkv", Size: 1024}},
	}}
	enhancer := &recognitionStub{err: errors.New("mock timeout")}
	p := planner.New(
		context.Background(),
		fs,
		1,
		planner.TaskConfig{TargetDirectoryID: "root", ActionType: "rename", MediaType: "movie", RenameMarker: "off", Recursive: true},
		planner.Settings{},
		"task-ai-fallback",
		nil,
		func(string) {},
		nil,
		func() error { return nil },
	)
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 1 || len(plan.Actions) != 0 || len(plan.Skipped) != 1 {
		t.Fatalf("AI 失败应局部降级: calls=%d actions=%+v skipped=%+v", enhancer.calls, plan.Actions, plan.Skipped)
	}
}

func TestPlannerRestoresOriginalTMDBMissWhenAIFails(t *testing.T) {
	fs := &mockFS{dirs: map[string][]domain.FileItem{
		"root": {{ID: "f1", Name: "Unknowable.Movie.2020.mkv", Size: 1024}},
	}}
	enhancer := &recognitionStub{err: errors.New("mock timeout")}
	p := newTestPlanner(fs, &mockTMDB{searchFn: func(string, *int) []map[string]any { return nil }}, "root")
	p.SetRecognitionEnhancer(enhancer)
	plan, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if enhancer.calls != 1 || len(plan.Actions) == 0 {
		t.Fatalf("AI 失败后应恢复原有无 TMDB 计划: calls=%d actions=%+v skipped=%+v", enhancer.calls, plan.Actions, plan.Skipped)
	}
	for _, action := range plan.Actions {
		if action.Metadata["recognition_source"] == "ai" {
			t.Fatalf("AI 失败后不应标记 AI 介入: %+v", action)
		}
	}
}
