package recognition

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("recognition enhancer unavailable")

type File struct {
	SourceID     string `json:"source_id"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type Work struct {
	WorkID          string `json:"work_id"`
	Directory       string `json:"directory,omitempty"`
	MediaTypeHint   string `json:"media_type_hint,omitempty"`
	CandidateTitle  string `json:"candidate_title,omitempty"`
	CandidateYear   *int   `json:"candidate_year,omitempty"`
	CandidateSeason *int   `json:"candidate_season,omitempty"`
	Files           []File `json:"files"`
}

type BatchRequest struct {
	Works []Work `json:"works"`
}

type FileResult struct {
	SourceID string `json:"source_id"`
	Episode  *int   `json:"episode,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type WorkResult struct {
	WorkID        string       `json:"work_id"`
	Recognized    bool         `json:"recognized"`
	Title         string       `json:"title,omitempty"`
	OriginalTitle string       `json:"original_title,omitempty"`
	Year          *int         `json:"year,omitempty"`
	MediaType     string       `json:"media_type,omitempty"`
	Season        *int         `json:"season,omitempty"`
	Files         []FileResult `json:"files,omitempty"`
}

// EpisodeRequest 只承载内置规则无法判断集数的文件。
// 作品身份已经由第一阶段确认，避免把整季文件重复交给 AI。
type EpisodeRequest struct {
	WorkID    string `json:"work_id"`
	Title     string `json:"title"`
	Year      *int   `json:"year,omitempty"`
	Season    *int   `json:"season,omitempty"`
	Directory string `json:"directory,omitempty"`
	Files     []File `json:"files"`
}

type BatchResult struct {
	Items  []WorkResult `json:"items"`
	Cached int          `json:"cached,omitempty"`
	Failed int          `json:"failed,omitempty"`
}

type BatchProgress struct {
	Total                 int
	Completed             int
	Cached                int
	Failed                int
	CurrentChunk          int
	TotalChunks           int
	CurrentBatchSize      int
	SplitDepth            int
	AttemptStartedAt      int64
	AttemptTimeoutSeconds int
	RetryingSmallerBatch  bool
}

type ProgressFunc func(BatchProgress)

type Enhancer interface {
	Available() bool
	Enhance(context.Context, BatchRequest) (BatchResult, error)
}

type ProgressEnhancer interface {
	Enhancer
	EnhanceWithProgress(context.Context, BatchRequest, ProgressFunc) (BatchResult, error)
}

// EpisodeResolver 是可选的文件级补判能力，仅在内置规则无法解析集数时调用。
type EpisodeResolver interface {
	ResolveEpisodes(context.Context, EpisodeRequest) ([]FileResult, error)
}
