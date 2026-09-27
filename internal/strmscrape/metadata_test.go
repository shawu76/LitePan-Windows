package strmscrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 构造一个「根已齐」的电影作品目录：<strm>.nfo + poster.jpg。
func newCompleteMovieWork(t *testing.T) workGroup {
	t.Helper()
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	write("阿凡达 (2009).strm")
	if err := os.WriteFile(filepath.Join(dir, "阿凡达 (2009).nfo"),
		[]byte("<movie><title>阿凡达</title></movie>\n"), 0o644); err != nil {
		t.Fatalf("写 NFO 失败: %v", err)
	}
	write("poster.jpg")
	strmPath := filepath.Join(dir, "阿凡达 (2009).strm")
	return workGroup{
		relKey:  "电影/阿凡达 (2009)",
		absDir:  dir,
		entries: []strmEntry{{absPath: strmPath}},
	}
}

func TestReadWorkNFOMetaRejectsWrongRoot(t *testing.T) {
	g := newCompleteMovieWork(t)
	path := filepath.Join(g.absDir, "阿凡达 (2009).nfo")
	if err := os.WriteFile(path, []byte("<tvshow><title>错误类型</title><tmdbid>1</tmdbid></tvshow>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readWorkNFOMeta(g, MediaTypeMovie); ok {
		t.Fatal("电影刮削不应读取 tvshow 根节点的 NFO")
	}
}

func allOptionalEnabled() Settings {
	return Settings{Fanart: true, ClearLogo: true, Actors: true, EpisodeInfo: true}
}

// writeDoneState 模拟「已刮削完结、TMDB 确认缺少全部可选资源」的落盘状态。
func writeDoneState(t *testing.T, g workGroup) {
	t.Helper()
	err := writePendingState(g, scrapeState{
		Status:     PendingDone,
		NoBackdrop: true,
		NoLogo:     true,
		NoActors:   true,
	})
	if err != nil {
		t.Fatalf("写状态失败: %v", err)
	}
}

func TestWorkNeedsScrapeRespectsMissingOptionalAssets(t *testing.T) {
	t.Run("TMDB 已确认没有则可选资源不再重刮", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if !workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("未记录结论时应因缺背景图/Logo/演员而需要刮削")
		}
		writeDoneState(t, g)
		if workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("已确认 TMDB 缺失时不应再刮削")
		}
	})

	t.Run("开关关闭时不因该资源重刮", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		cfg := Settings{Fanart: false, ClearLogo: false, Actors: false}
		if workNeedsScrapeFromInspection(g, cfg, inspectWork(g)) {
			t.Fatalf("三个可选开关都关闭时，根齐全即应跳过")
		}
	})

	t.Run("本地已有资源则忽略结论", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if err := os.WriteFile(filepath.Join(g.absDir, "fanart.jpg"), []byte("x"), 0o644); err != nil {
			t.Fatalf("写背景图失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(g.absDir, "clearlogo.png"), []byte("x"), 0o644); err != nil {
			t.Fatalf("写 Logo 失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(g.absDir, "阿凡达 (2009).nfo"), []byte("<movie><actor><name>A</name></actor></movie>"), 0o644); err != nil {
			t.Fatalf("写 NFO 失败: %v", err)
		}
		writeDoneState(t, g)
		if workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("资源齐全后不应再刮削")
		}
	})

	t.Run("只记一项时其余项仍会触发", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if err := writePendingState(g, scrapeState{Status: PendingDone, NoLogo: true}); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		if !workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("未记录结论的背景图缺失仍应触发刮削")
		}
		if err := writePendingState(g, scrapeState{Status: PendingDone, NoLogo: true, NoBackdrop: true}); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		if !workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("未记录结论的演员缺失仍应触发刮削")
		}
	})

	t.Run("done 不视为待刮削", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		writeDoneState(t, g)
		item := buildItem(1, g.absDir, g)
		if item.HasPending {
			t.Fatalf("done 状态不应上报 HasPending")
		}
		if item.Status != ItemStatusOK {
			t.Fatalf("根已齐且 done 时应为 OK，实际 %q", item.Status)
		}
	})

	t.Run("真实 pending 仍然要刮", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if err := writePendingState(g, scrapeState{Status: PendingDoubt}); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		if !workNeedsScrapeFromInspection(g, Settings{}, inspectWork(g)) {
			t.Fatalf("存疑状态应继续刮削")
		}
	})

	t.Run("手动完成标记仍优先", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if err := writeManualComplete(g, MediaTypeMovie); err != nil {
			t.Fatalf("写手动完成标记失败: %v", err)
		}
		if workNeedsScrapeFromInspection(g, allOptionalEnabled(), inspectWork(g)) {
			t.Fatalf("手动标记无需匹配时不应刮削")
		}
	})
}

func TestSyncOptionalAssetState(t *testing.T) {
	t.Run("TMDB 无资源时写入 done 结论", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		syncOptionalAssetState(g, allOptionalEnabled(), tmdbInfo{TMDBID: "76600"}, false)
		st, ok := readPendingState(g)
		if !ok || st.Status != PendingDone {
			t.Fatalf("应写入 done 状态，实际 %+v ok=%v", st, ok)
		}
		if !st.NoBackdrop || !st.NoLogo || !st.NoActors {
			t.Fatalf("应记录三项缺失，实际 %+v", st)
		}
	})

	t.Run("TMDB 有资源时删除文件", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		writeDoneState(t, g)
		info := tmdbInfo{TMDBID: "76600", BackdropPath: "/b.jpg", LogoPath: "/l.png"}
		info.Actors = []tmdbActor{{Name: "Sam Worthington"}}
		syncOptionalAssetState(g, allOptionalEnabled(), info, false)
		if _, ok := readPendingState(g); ok {
			t.Fatalf("无缺失结论时不应保留标记文件")
		}
	})

	t.Run("开关关闭时不写入该资源结论", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		syncOptionalAssetState(g, Settings{Actors: true}, tmdbInfo{TMDBID: "76600"}, false)
		st, ok := readPendingState(g)
		if !ok {
			t.Fatalf("应写入标记")
		}
		if st.NoBackdrop || st.NoLogo {
			t.Fatalf("未检查的资源不应被记录，实际 %+v", st)
		}
		if !st.NoActors {
			t.Fatalf("开启的演员项缺失应被记录，实际 %+v", st)
		}
	})

	t.Run("追更中的 pending 不被改写", func(t *testing.T) {
		g := newCompleteMovieWork(t)
		if err := writePendingState(g, scrapeState{Status: PendingUpdating, EpLocal: 3, EpTMDB: 10}); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		syncOptionalAssetState(g, allOptionalEnabled(), tmdbInfo{TMDBID: "76600"}, false)
		st, _ := readPendingState(g)
		if st.Status != PendingUpdating || st.EpTMDB != 10 {
			t.Fatalf("追更状态不应被覆盖，实际 %+v", st)
		}
		if st.NoBackdrop {
			t.Fatalf("追更未完结时不应写入可选资源结论，实际 %+v", st)
		}
	})
}

func TestMarkNormalStopsAutoScrape(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "开局女帝盯上了我的彩礼 (2024)")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	mustWrite(t, filepath.Join(s1, "开局女帝.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>开局女帝</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := allOptionalEnabled()
	// 未设为完结：根齐但缺背景图/Logo/演员 → 仍需刮削。
	if !workNeedsScrapeFromInspection(works[0], cfg, inspectWork(works[0])) {
		t.Fatalf("根齐但可选资源缺失时应刮削")
	}
	if err := markWorkNormal(works[0], MediaTypeTV); err != nil {
		t.Fatalf("设为完结失败: %v", err)
	}
	if workNeedsScrapeFromInspection(works[0], cfg, inspectWork(works[0])) {
		t.Fatal("设为完结后不应再自动刮削（即使缺背景图/Logo/演员）")
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusOK || item.TVState != TVStateEnded || item.HasPending {
		t.Fatalf("status=%s tv_state=%s has_pending=%v", item.Status, item.TVState, item.HasPending)
	}

	t.Run("手动重刮后按最新 TMDB 结果覆写终态", func(t *testing.T) {
		// TMDB 仍没有可选资源 → 记为 done 结论，之后不再重复刮削。
		syncOptionalAssetState(works[0], cfg, tmdbInfo{TMDBID: "1"}, false)
		if st, ok := readPendingState(works[0]); !ok || st.Status != PendingDone || !st.NoLogo {
			t.Fatalf("应写入 done + 缺失结论，实际 %+v ok=%v", st, ok)
		}
		if workNeedsScrapeFromInspection(works[0], cfg, inspectWork(works[0])) {
			t.Fatal("已记结论后不应再刮削")
		}
	})
}

func TestClearScrapedMetadataClearsState(t *testing.T) {
	g := newCompleteMovieWork(t)
	writeDoneState(t, g)
	if err := clearScrapedMetadata(g); err != nil {
		t.Fatalf("清理元数据失败: %v", err)
	}
	if _, ok := readPendingState(g); ok {
		t.Fatal("清理元数据后不应保留终态或结论")
	}
}

// 压制组随片发布的 MediaInfo 文本，会被元数据同步复制成 .nfo，但不是标准 NFO。
const sceneMediaInfoNFO = `THEATRE DATE....: 2023
iMDB URL........: http://www.imdb.com/title/tt22488024/
GENRE...........: Action, Adventure
ViDEO BiTRATE...: x265 L5 Main 10 @ 27.5 Mbps
FilE SiZE.......: 34.33 G
`

func TestNFOLooksStandard(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
		return path
	}

	if nfoLooksStandard(filepath.Join(dir, "不存在.nfo")) {
		t.Fatal("文件不存在不应算标准 NFO")
	}
	if nfoLooksStandard(write("scene.nfo", sceneMediaInfoNFO)) {
		t.Fatal("MediaInfo 文本不应算标准 NFO")
	}
	if !nfoLooksStandard(write("movie.nfo", "<?xml version=\"1.0\"?>\n<movie><title>A</title></movie>\n")) {
		t.Fatal("带 XML 声明的 movie 根应算标准 NFO")
	}
	if !nfoLooksStandard(write("upper.nfo", "<MOVIE><TITLE>A</TITLE></MOVIE>")) {
		t.Fatal("大写根节点应算标准 NFO")
	}
	if !nfoLooksStandard(write("tv.nfo", "<tvshow><title>S</title></tvshow>")) {
		t.Fatal("tvshow 根应算标准 NFO")
	}
	if nfoLooksStandard(write("ep.nfo", "<episodedetails><title>E1</title></episodedetails>")) {
		t.Fatal("分集 NFO 不是作品级 NFO")
	}
}

func TestNFOOverwriteNonStandard(t *testing.T) {
	year := 2023
	dir := t.TempDir()
	nfo := filepath.Join(dir, "天龙八部之乔峰传 (2023).nfo")
	if err := os.WriteFile(nfo, []byte(sceneMediaInfoNFO), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	// 非标准 NFO 直接重写（覆盖），不保留备份。
	if !nfoWriteNeeded(false, nfo) {
		t.Fatal("非标准 NFO 应重写")
	}
	if err := writeMovieNFO(nfo, "天龙八部之乔峰传", "22488024", "剧情", &year); err != nil {
		t.Fatalf("写 NFO 失败: %v", err)
	}
	if !nfoLooksStandard(nfo) {
		t.Fatal("重写后应为标准 NFO")
	}
	if fileExists(nfo + ".scene.bak") {
		t.Fatal("不应保留备份文件")
	}
	// 已是标准 NFO 且非覆盖模式：不再重写。
	if nfoWriteNeeded(false, nfo) {
		t.Fatal("标准 NFO 且非覆盖模式不应重写")
	}
	if !nfoWriteNeeded(true, nfo) {
		t.Fatal("覆盖模式应重写")
	}
}

func TestSceneNFOIsNotTreatedAsMetadata(t *testing.T) {
	g := newCompleteMovieWork(t)
	// 用压制组信息文件替换标准 NFO，模拟「元数据同步」把网盘上的 .nfo 覆盖成了非标准文件。
	nfo := filepath.Join(g.absDir, "阿凡达 (2009).nfo")
	if err := os.WriteFile(nfo, []byte(sceneMediaInfoNFO), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if workHasNFO(g, MediaTypeMovie) {
		t.Fatal("非标准 .nfo 不应被当作已有元数据")
	}
	if workHasActors(g, MediaTypeMovie) {
		t.Fatal("非标准 .nfo 不应参与演员判定")
	}
	if !workNeedsScrapeFromInspection(g, Settings{}, inspectWork(g)) {
		t.Fatal("缺标准 NFO 时应重新刮削（会直接覆盖非标准文件）")
	}
	if !workHasPoster(g, MediaTypeMovie) {
		t.Fatal("海报不受影响")
	}
}

type stubImageDownloader struct {
	data []byte
	err  error
}

func (d stubImageDownloader) DownloadImage(context.Context, string, string) ([]byte, error) {
	return d.data, d.err
}

func TestWriteOptionalArtworkSkipsDownloadFailure(t *testing.T) {
	var logs bytes.Buffer
	svc := &Service{log: slog.New(slog.NewTextHandler(&logs, nil))}
	out := filepath.Join(t.TempDir(), "episode-thumb.jpg")

	written, err := svc.writeOptionalArtwork(context.Background(), stubImageDownloader{err: fmt.Errorf("图片 404")}, "/missing.jpg", out, "S01E275 缩略图")
	if err != nil {
		t.Fatalf("可选图片下载失败不应中断刮削，err=%v", err)
	}
	if written {
		t.Fatal("下载失败不应报告已写入")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("下载失败后不应生成图片，err=%v", err)
	}
	if text := logs.String(); !strings.Contains(text, "可选图片下载失败") || !strings.Contains(text, "S01E275 缩略图") {
		t.Fatalf("未记录可选图片警告：%s", text)
	}
}

func TestWriteOptionalArtworkPreservesWriteFailure(t *testing.T) {
	root := t.TempDir()
	notDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &Service{}
	_, err := svc.writeOptionalArtwork(context.Background(), stubImageDownloader{data: []byte("image")}, "/ok.jpg", filepath.Join(notDir, "thumb.jpg"), "S01E275 缩略图")
	if err == nil || !strings.Contains(err.Error(), "写入S01E275 缩略图") {
		t.Fatalf("本地写入失败必须保留，err=%v", err)
	}
}

func TestWriteOptionalArtworkPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &Service{}
	_, err := svc.writeOptionalArtwork(ctx, stubImageDownloader{err: fmt.Errorf("请求失败")}, "/cancel.jpg", filepath.Join(t.TempDir(), "thumb.jpg"), "S01E275 缩略图")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("任务取消不能被降级为警告，err=%v", err)
	}
}
