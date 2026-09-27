package rules

import "testing"

func TestParseDirAndFileNames(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		useFile   bool
		wantTitle string
		wantYear  *int
	}{
		{
			name:      "anime bracket dir",
			input:     "[4K][DBD-Raws&诸神字幕组][千与千寻][2160P][BDRip][简繁中日内封][FLAC].mkv",
			wantTitle: "千与千寻",
		},
		{
			name:      "chinese quality dir",
			input:     "千与千寻 蓝光原盘REMUX 国日双音 内封简日字幕",
			wantTitle: "千与千寻",
		},
		{
			name:      "bracket movie file",
			input:     "[爱乐之城 La La Land 2016][DIY简繁双语特效字幕][bb@HDSky][46.36GB].iso",
			useFile:   true,
			wantTitle: "爱乐之城 La La Land",
			wantYear:  intPtr(2016),
		},
		{
			name:      "simple chinese dir",
			input:     "暗战",
			wantTitle: "暗战",
		},
		{
			name:      "title with year paren",
			input:     "731 (2025)",
			wantTitle: "731",
			wantYear:  intPtr(2025),
		},
		{
			name:      "bare episode file",
			input:     "01.mp4",
			useFile:   true,
			wantTitle: "",
		},
		{
			name:      "bare episode with quality",
			input:     "01 4K.mp4",
			useFile:   true,
			wantTitle: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ParsedMedia
			if tt.useFile {
				got = NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			} else {
				got = NormalizeParsedMedia(ParseDirName(tt.input))
			}
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Year, tt.wantYear) {
				t.Fatalf("year = %v, want %v (full=%+v)", got.Year, tt.wantYear, got)
			}
			if tt.name == "bare episode file" {
				if got.Episode == nil || *got.Episode != 1 {
					t.Fatalf("episode = %v, want 1 (full=%+v)", got.Episode, got)
				}
			}
			if tt.name == "bare episode with quality" {
				if got.Episode == nil || *got.Episode != 1 {
					t.Fatalf("episode = %v, want 1 (full=%+v)", got.Episode, got)
				}
				if got.ScreenSize != "2160p" {
					t.Fatalf("screen_size = %q, want 2160p (full=%+v)", got.ScreenSize, got)
				}
			}
		})
	}
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func TestReleaseGroupAndSubtitleStripping(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantTitle        string
		wantYear         *int
		wantReleaseGroup string
	}{
		{
			name:             "chinese release group tail cmct",
			input:            "千与千寻.2019.1080p.WEB-DL.x264-CMCT.mkv",
			wantTitle:        "千与千寻",
			wantYear:         intPtr(2019),
			wantReleaseGroup: "CMCT",
		},
		{
			name:             "chinese release group tail beast",
			input:            "The.Matrix.1999.1080p.BluRay.x264-beAst.mkv",
			wantTitle:        "The Matrix",
			wantYear:         intPtr(1999),
			wantReleaseGroup: "beAst",
		},
		{
			name:             "anime ascii sub group tail sweetsub",
			input:            "Spy.x.Family.2022.1080p.WEB-DL.AAC2.0.H.264-SweetSub.mkv",
			wantTitle:        "Spy x Family",
			wantYear:         intPtr(2022),
			wantReleaseGroup: "SweetSub",
		},
		{
			name:      "anime bracket loli house",
			input:     "[LoliHouse] 孤独摇滚！ - 01 [WebRip 1080p HEVC-10bit AAC][简繁内封字幕].mkv",
			wantTitle: "孤独摇滚！",
		},
		{
			name:      "anime bracket kitauji sub",
			input:     "[北宇治字幕组] 吹响吧！上低音号 [1080P].mkv",
			wantTitle: "吹响吧！上低音号",
		},
		{
			name:      "anime bracket gisou sub",
			input:     "【极影字幕社】我的青春恋爱物语果然有问题 [01].mkv",
			wantTitle: "我的青春恋爱物语果然有问题",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Year, tt.wantYear) {
				t.Fatalf("year = %v, want %v (full=%+v)", got.Year, tt.wantYear, got)
			}
			if got.ReleaseGroup != tt.wantReleaseGroup {
				t.Fatalf("release_group = %q, want %q (full=%+v)", got.ReleaseGroup, tt.wantReleaseGroup, got)
			}
		})
	}
}

func TestParseFilenameWithStackedEquivalentEpisodeMarkers(t *testing.T) {
	for _, tt := range []struct {
		name    string
		episode int
	}{
		{"中国奇谭 - S01E06 - 第 6 集.mkv", 6},
		{"中国奇谭 - S01E07 - 第 7 集.mkv", 7},
	} {
		got := NormalizeParsedMedia(ParseFilenameStrict(tt.name))
		if got.Title != "中国奇谭" || got.Season == nil || *got.Season != 1 || got.Episode == nil || *got.Episode != tt.episode {
			t.Fatalf("堆叠集号文件名解析不完整：name=%q got=%+v", tt.name, got)
		}
	}
}

func TestParseNotMisjudgeVideoCodecAsEpisode(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantEp    *int
	}{
		{
			name:      "h265 not episode 265",
			input:     "Some.Show.S01.2160p.WEB-DL.H265.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "x265 not episode 265",
			input:     "Some.Show.S01.2160p.WEB-DL.x265.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "hevc not episode 265",
			input:     "Some.Show.S01.1080p.BluRay.HEVC.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "avc not episode 264",
			input:     "Some.Show.S01.1080p.AVC.mkv",
			wantTitle: "Some Show",
		},
		{
			name:      "real episode still kept",
			input:     "Some.Show.S01E05.2160p.WEB-DL.H265.mkv",
			wantTitle: "Some Show",
			wantEp:    intPtr(5),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Episode, tt.wantEp) {
				t.Fatalf("episode = %v, want %v (full=%+v)", got.Episode, tt.wantEp, got)
			}
			if tt.wantEp == nil && got.Episode != nil && (*got.Episode == 264 || *got.Episode == 265) {
				t.Fatalf("video codec number misjudged as episode: %v (full=%+v)", *got.Episode, got)
			}
		})
	}
}

func TestParseSeasonOnly(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTitle  string
		wantSeason *int
	}{
		{
			name:       "s01 only",
			input:      "Some.Show.S01.2160p.WEB-DL.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(1),
		},
		{
			name:       "season 2 only",
			input:      "Some.Show.Season 2.1080p.BluRay.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(2),
		},
		{
			name:       "chinese season only",
			input:      "Some.Show.第2季.1080p.WEB-DL.mkv",
			wantTitle:  "Some Show",
			wantSeason: intPtr(2),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeParsedMedia(ParseFilenameStrict(tt.input))
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q (full=%+v)", got.Title, tt.wantTitle, got)
			}
			if !intPtrEqual(got.Season, tt.wantSeason) {
				t.Fatalf("season = %v, want %v (full=%+v)", got.Season, tt.wantSeason, got)
			}
			if got.Type != "episode" {
				t.Fatalf("type = %q, want episode (full=%+v)", got.Type, got)
			}
		})
	}
}

func TestStripChineseBracketTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// 标签被替换为单个空格，防止相邻文本粘连（如 [全10集]第一季 不会拼成 第一季前贴片名）
		{name: "full episode tag", input: "片名[全10集].mkv", want: "片名 .mkv"},
		{name: "subtitle tag", input: "片名[内封简英字幕].mkv", want: "片名 .mkv"},
		{name: "corner bracket ad", input: "片名【广告】.mkv", want: "片名 .mkv"},
		{name: "keep quality bracket", input: "片名[2160p].mkv", want: "片名[2160p].mkv"},
		{name: "keep bare episode bracket", input: "片名[01].mkv", want: "片名[01].mkv"},
		{name: "empty input", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripChineseBracketTags(tt.input); got != tt.want {
				t.Fatalf("StripChineseBracketTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseChineseBracketTagsStripped(t *testing.T) {
	// 集成：ParseFilenameStrict 解析时中文标签不进入标题
	got := NormalizeParsedMedia(ParseFilenameStrict("片名[全10集][内封简英字幕].mkv"))
	if got.Title != "片名" {
		t.Fatalf("title = %q, want 片名 (full=%+v)", got.Title, got)
	}
}

func TestParseSeasonInfoInMiddle(t *testing.T) {
	// 季信息在中间（非末尾，走 ParseDirName）：片名.第二季[全26集]…2024… → title=片名 season=2
	for _, input := range []string{
		"片名.第二季[全26集]2024.1080p.WEB-DL.mkv",
		"片名.第二季[全26集]1080p.WEB-DL.mkv",
		"片名.第二季.1080p.WEB-DL.mkv",
	} {
		got := NormalizeParsedMedia(ParseDirName(input))
		if got.Title != "片名" {
			t.Fatalf("ParseDirName(%q) title = %q, want 片名 (full=%+v)", input, got.Title, got)
		}
		if !intPtrEqual(got.Season, intPtr(2)) {
			t.Fatalf("ParseDirName(%q) season = %v, want 2 (full=%+v)", input, got.Season, got)
		}
		if got.Type != "episode" {
			t.Fatalf("ParseDirName(%q) type = %q, want episode (full=%+v)", input, got.Type, got)
		}
	}
}
