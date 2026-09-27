package strmscrape

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskIndexPathAndRemove(t *testing.T) {
	dir := t.TempDir()
	path := TaskIndexPath(dir, 42)
	want := filepath.Join(dir, "strmscrape", "42.sqlite")
	if path != want {
		t.Fatalf("path=%s want %s", path, want)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path+"-wal", []byte("w"), 0o644)
	RemoveTaskIndex(dir, 42)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("index should be removed, err=%v", err)
	}
}

func TestIndexUpsertAndList(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{dataDir: dir}
	path := svc.indexPath(9)
	db, err := openTaskIndexDB(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	year := 2021
	it := Item{
		ID: "abc", Title: "天龙八部", Year: &year, MediaType: MediaTypeTV,
		Status: ItemStatusOK, HasNFO: true, HasPoster: true, HasPending: true, ManualDone: true,
		TMDBID: "1", FolderName: "天龙八部 (2021)", FileCount: 2,
		EpLocal: 1, EpTMDB: 40, TVState: TVStateUpdating, AddedAt: "2026-01-01T00:00:00Z",
	}
	if err := upsertItemTx(tx, it, "poster.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := writeIndexMeta(tx, "schema", indexSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := writeIndexMeta(tx, "root", "/tmp/out"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	result, err := svc.listIndexItems(9, ItemListQuery{
		Limit: defaultItemListLimit,
		Sort:  ItemListSortAddedDesc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Stats.Total != 1 || result.Stats.OK != 1 {
		t.Fatalf("result=%+v", result)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len=%d", len(result.Items))
	}
	got := result.Items[0]
	if got.Title != "天龙八部" || got.EpTMDB != 40 || !got.HasPending || !got.ManualDone {
		t.Fatalf("got=%+v", got)
	}
	if got.Year == nil || *got.Year != 2021 {
		t.Fatalf("year=%v", got.Year)
	}
	if got.PosterURL == "" || !got.HasPoster {
		t.Fatalf("poster url empty: %s", got.PosterURL)
	}
}

func TestListIndexItemsQuery(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{dataDir: dir}
	path := svc.indexPath(10)
	db, err := openTaskIndexDB(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	year2024 := 2024
	year2023 := 2023
	items := []Item{
		{
			ID: "movie-1", Title: "Alpha Movie", Year: &year2024, MediaType: MediaTypeMovie,
			Status: ItemStatusMiss, FolderName: "Alpha Movie (2024)", AddedAt: "2026-01-03T00:00:00Z",
		},
		{
			ID: "tv-1", Title: "Bravo Show", Year: &year2023, MediaType: MediaTypeTV,
			Status: ItemStatusOK, FolderName: "Bravo Show (2023)", TVState: TVStateUpdating,
			AddedAt: "2026-01-02T00:00:00Z",
		},
		{
			ID: "tv-2", Title: "Charlie Show", MediaType: MediaTypeTV,
			Status: ItemStatusDoubt, FolderName: "Charlie Show", TVState: TVStateEnded,
			AddedAt: "2026-01-01T00:00:00Z",
		},
	}
	for _, item := range items {
		if err := upsertItemTx(tx, item, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeIndexMeta(tx, "schema", indexSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := writeIndexMeta(tx, "root", "/tmp/out"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	result, err := svc.listIndexItems(10, ItemListQuery{
		Limit:     1,
		MediaType: MediaTypeTV,
		Sort:      ItemListSortTitleAsc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || !result.HasMore || len(result.Items) != 1 {
		t.Fatalf("page result=%+v", result)
	}
	if result.Items[0].Title != "Bravo Show" {
		t.Fatalf("first title=%s", result.Items[0].Title)
	}
	if result.Stats.Total != 3 || result.Stats.OK != 1 || result.Stats.Miss != 1 || result.Stats.Doubt != 1 {
		t.Fatalf("stats=%+v", result.Stats)
	}

	result, err = svc.listIndexItems(10, ItemListQuery{
		Limit:     defaultItemListLimit,
		Offset:    1,
		MediaType: MediaTypeTV,
		Sort:      ItemListSortTitleAsc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "Charlie Show" || result.HasMore {
		t.Fatalf("offset result=%+v", result)
	}

	result, err = svc.listIndexItems(10, ItemListQuery{
		Limit:   defaultItemListLimit,
		Keyword: "alpha",
		Sort:    ItemListSortAddedDesc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != "movie-1" {
		t.Fatalf("keyword result=%+v", result)
	}
}

func TestNormalizeScopeDirsRemovesDuplicatesAndCoveredChildren(t *testing.T) {
	got := normalizeScopeDirs([]string{" 电影/临时 ", "电影", "电影/临时", "../非法", "综艺"})
	want := []string{"电影", "综艺"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestFilterWorksByScope(t *testing.T) {
	works := []workGroup{
		{relKey: "电影/阿凡达 (2009)"},
		{relKey: "电视剧/藏锋 (2026)"},
		{relKey: "临时测试/片段"},
	}
	got := filterWorksByScope(works, []string{"临时测试", "电影/阿凡达 (2009)"})
	if len(got) != 1 || got[0].relKey != "电视剧/藏锋 (2026)" {
		t.Fatalf("unexpected works: %#v", got)
	}
}

func TestRelUnderAbsRootWithRelativeFull(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "strm_out")
	show := filepath.Join(root, "航海王 (1999)")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	posterAbs := filepath.Join(show, "poster.jpg")
	if err := os.WriteFile(posterAbs, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	posterRel, err := filepath.Rel(cwd, posterAbs)
	if err != nil {
		t.Fatal(err)
	}
	got := relUnder(root, posterRel)
	want := filepath.ToSlash(filepath.Join("航海王 (1999)", "poster.jpg"))
	if filepath.ToSlash(got) != want {
		t.Fatalf("relUnder=%q want %q（绝对 root + 相对 full 不应退化成 basename）", got, want)
	}
}
