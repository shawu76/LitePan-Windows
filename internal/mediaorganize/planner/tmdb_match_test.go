package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/rules"
)

type fallbackTMDBStub int

func TestPlanMovieDoesNotUseEpisodeNumbers(t *testing.T) {
	for _, mode := range []string{"rename", "move"} {
		for _, kind := range []string{"movie", "tv"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				p := New(context.Background(), nil, 1, TaskConfig{
					TargetDirectoryID: "root", TargetRootID: "target", ActionType: mode, RenameMarker: "tmdb",
				}, nil, "task", nil, nil, nil, nil)
				key := groupKey{mediaKind: kind, dirID: "work", dirName: "钢铁侠3", title: "钢铁侠3"}
				key.setSeason(intPtr(1))
				key.setEpisode(intPtr(3))
				entry := batchEntry{
					item: domain.FileItem{ID: "file", Name: "Iron.Man.3.mkv"}, sourceDirID: "work",
					fileParsed: rules.ParsedMedia{Title: "Iron Man", Season: intPtr(1), Episode: intPtr(3)},
				}
				match := tmdbMatchResult{tmdbID: "68721", title: "钢铁侠3", tmdbTitle: "钢铁侠3", year: intPtr(2013)}
				if err := p.planGroupWithMatch(key, []batchEntry{entry}, nil, &match, false); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, action := range p.actions {
					if action.SourceID != "file" {
						continue
					}
					found = true
					if got := strings.Contains(action.TargetName, "S01E03"); got != (kind == "tv") {
						t.Fatalf("媒体类型 %s 的季集命名错误: %q", kind, action.TargetName)
					}
					if kind == "movie" && (rules.AsFirstInt(action.Metadata["season"]) != nil || rules.AsFirstInt(action.Metadata["episode"]) != nil) {
						t.Fatalf("电影动作不应残留季集信息: %+v", action.Metadata)
					}
				}
				if !found {
					t.Fatalf("没有生成文件整理动作: %+v", p.actions)
				}
			})
		}
	}
}

func (*fallbackTMDBStub) ValidateConnection(context.Context) bool { return true }

func (s *fallbackTMDBStub) Search(context.Context, string, *int, string) ([]json.RawMessage, error) {
	*s++
	return []json.RawMessage{json.RawMessage(`{"id":1001,"title":"测试电影","release_date":"2010-01-01"}`)}, nil
}

type seasonAwareTMDBStub struct {
	queries []string
}

func (*seasonAwareTMDBStub) ValidateConnection(context.Context) bool { return true }
func (s *seasonAwareTMDBStub) Search(_ context.Context, query string, year *int, mediaType string) ([]json.RawMessage, error) {
	yearText := ""
	if year != nil {
		yearText = fmt.Sprintf(":%d", *year)
	}
	s.queries = append(s.queries, mediaType+":"+query+yearText)
	if mediaType != "tv" {
		return nil, nil
	}
	return []json.RawMessage{
		json.RawMessage(`{"id":196615,"name":"我叫赵甲第","first_air_date":"2022-03-31"}`),
		json.RawMessage(`{"id":282896,"name":"我叫赵甲第之锋芒","first_air_date":"2025-05-01"}`),
		json.RawMessage(`{"id":315013,"name":"我叫赵甲第 第二季","first_air_date":"2025-06-01"}`),
		json.RawMessage(`{"id":999999,"name":"我叫赵甲第","first_air_date":"2026-01-01"}`),
	}, nil
}
func (*seasonAwareTMDBStub) Lookup(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (*seasonAwareTMDBStub) FetchTVSeasons(_ context.Context, id string) ([]json.RawMessage, error) {
	switch id {
	case "196615":
		return []json.RawMessage{
			json.RawMessage(`{"season_number":1,"air_date":"2022-03-31"}`),
			json.RawMessage(`{"season_number":2,"air_date":"2025-04-28"}`),
		}, nil
	case "999999":
		return []json.RawMessage{json.RawMessage(`{"season_number":2,"air_date":"2025-01-01"}`)}, nil
	default:
		return []json.RawMessage{json.RawMessage(`{"season_number":1,"air_date":"2025-01-01"}`)}, nil
	}
}

type noVerifiedSeasonTMDBStub struct{ seasonAwareTMDBStub }

func (*noVerifiedSeasonTMDBStub) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	return []json.RawMessage{json.RawMessage(`{"season_number":1,"air_date":"2025-01-01"}`)}, nil
}

func (*fallbackTMDBStub) Lookup(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (*fallbackTMDBStub) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	return nil, nil
}

type trailingNumberTMDBStub struct {
	queries []string
}

type aliasTMDBStub struct{}

func (*aliasTMDBStub) ValidateConnection(context.Context) bool { return true }

func (*aliasTMDBStub) Search(_ context.Context, query string, _ *int, mediaType string) ([]json.RawMessage, error) {
	if mediaType == "tv" && query == "海贼王" {
		return []json.RawMessage{json.RawMessage(`{"id":37854,"name":"航海王","original_name":"ワンピース","first_air_date":"1999-10-20"}`)}, nil
	}
	return nil, nil
}

func (*aliasTMDBStub) Lookup(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (*aliasTMDBStub) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	return nil, nil
}

func (*trailingNumberTMDBStub) ValidateConnection(context.Context) bool { return true }

func (s *trailingNumberTMDBStub) Search(_ context.Context, query string, _ *int, mediaType string) ([]json.RawMessage, error) {
	s.queries = append(s.queries, mediaType+":"+query)
	switch mediaType + ":" + query {
	case "movie:测试电影":
		return []json.RawMessage{json.RawMessage(`{"id":2001,"title":"测试电影","release_date":"2020-01-01"}`)}, nil
	case "tv:测试剧":
		return []json.RawMessage{json.RawMessage(`{"id":3001,"name":"测试剧","first_air_date":"2020-01-01"}`)}, nil
	default:
		return nil, nil
	}
}

func (*trailingNumberTMDBStub) Lookup(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (*trailingNumberTMDBStub) FetchTVSeasons(context.Context, string) ([]json.RawMessage, error) {
	return nil, nil
}

func TestMatchTMDBForGroupRejectsYearMismatchWithoutRepeatedQuery(t *testing.T) {
	year := 2020
	var tmdb fallbackTMDBStub
	p := &Planner{ctx: context.Background(), tmdb: &tmdb, log: func(string) {}}

	result, err := p.matchTMDBForGroup(groupKey{mediaKind: "movie", title: "测试电影", year: year, hasYear: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.tmdbID != "" {
		t.Fatalf("明确年份不符时不应采用候选，实际 tmdb id=%q", result.tmdbID)
	}
	if tmdb != 2 {
		t.Fatalf("同一候选应只执行带年份和不带年份两次查询，实际 %d 次", tmdb)
	}
}

func TestMatchTMDBForGroupAcceptsTMDBAliasWithExactYear(t *testing.T) {
	year := 1999
	p := &Planner{ctx: context.Background(), tmdb: &aliasTMDBStub{}, log: func(string) {}}
	result, err := p.matchTMDBForGroup(groupKey{mediaKind: "tv", title: "海贼王", year: year, hasYear: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.tmdbID != "37854" || result.tmdbTitle != "航海王" {
		t.Fatalf("海贼王 (1999) 应通过 TMDB 别名搜索命中航海王，实际=%+v", result)
	}
}

func TestTrailingNumberFallbackOnlyAppliesToTV(t *testing.T) {
	movieTMDB := &trailingNumberTMDBStub{}
	moviePlanner := &Planner{ctx: context.Background(), tmdb: movieTMDB, log: func(string) {}}
	movieResult, err := moviePlanner.matchTMDBForGroup(groupKey{mediaKind: "movie", title: "测试电影2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if movieResult.tmdbID != "" {
		t.Fatalf("电影尾数字不应被剔除后命中前作，实际 tmdb id=%q", movieResult.tmdbID)
	}
	for _, query := range movieTMDB.queries {
		if query == "movie:测试电影" {
			t.Fatalf("电影不应搜索剔除尾数字后的标题，查询=%v", movieTMDB.queries)
		}
	}

	tvTMDB := &trailingNumberTMDBStub{}
	tvPlanner := &Planner{ctx: context.Background(), tmdb: tvTMDB, log: func(string) {}}
	tvResult, err := tvPlanner.matchTMDBForGroup(groupKey{mediaKind: "tv", title: "测试剧2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tvResult.tmdbID != "3001" {
		t.Fatalf("电视剧应保留尾数字季号回退，实际 tmdb id=%q", tvResult.tmdbID)
	}
	if season, ok := tvResult.inferredSeason.(int); !ok || season != 2 {
		t.Fatalf("电视剧尾数字应推断为第 2 季，实际=%v", tvResult.inferredSeason)
	}
}

func TestMatchTMDBForGroupUsesExplicitSeasonAndSeasonYear(t *testing.T) {
	year := 2025
	season := 2
	tmdb := &seasonAwareTMDBStub{}
	p := &Planner{
		ctx: context.Background(), tmdb: tmdb, log: func(string) {},
		tvSeasonsCache: make(map[string][]map[string]any),
	}
	result, err := p.matchTMDBForGroup(groupKey{
		mediaKind: "tv", title: "我叫赵甲第", dirName: "我叫赵甲第 (2025)",
		year: year, hasYear: true,
	}, []batchEntry{{
		item:       domain.FileItem{ID: "e1", Name: "我叫赵甲第2.2025.S02E01.1080p.WEB-DL.H264.AAC5.1.mkv"},
		fileParsed: rules.ParsedMedia{Title: "我叫赵甲第2", Year: &year, Season: &season, Episode: intPtr(1), Type: "episode"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.tmdbID != "196615" {
		t.Fatalf("应根据 Season 02 (2025) 命中原剧 tmdb-196615，实际=%+v", result)
	}
	if result.year == nil || *result.year != 2022 {
		t.Fatalf("整剧目录应使用首播年份 2022，实际=%v", result.year)
	}
	if got, ok := result.inferredSeason.(int); !ok || got != 2 {
		t.Fatalf("应保留第 2 季，实际=%v", result.inferredSeason)
	}
	for _, query := range tmdb.queries {
		if strings.Contains(query, ":2025") {
			t.Fatalf("季度感知搜索不应用 2025 限制整剧首播年份，查询=%v", tmdb.queries)
		}
	}
}

func TestMatchTMDBForGroupDoesNotFallbackWhenSeasonEvidenceConflicts(t *testing.T) {
	year := 2025
	season := 2
	p := &Planner{
		ctx: context.Background(), tmdb: &noVerifiedSeasonTMDBStub{}, log: func(string) {},
		tvSeasonsCache: make(map[string][]map[string]any),
	}
	result, err := p.matchTMDBForGroup(groupKey{
		mediaKind: "tv", title: "我叫赵甲第", dirName: "我叫赵甲第 (2025)",
		year: year, hasYear: true,
	}, []batchEntry{{
		item:       domain.FileItem{ID: "e1", Name: "我叫赵甲第2.2025.S02E01.mkv"},
		fileParsed: rules.ParsedMedia{Season: &season, Episode: intPtr(1), Type: "episode"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.tmdbID != "" {
		t.Fatalf("所有候选都没有 Season 02 (2025) 时应留待人工匹配，不应仅凭同年命中: %+v", result)
	}
}
