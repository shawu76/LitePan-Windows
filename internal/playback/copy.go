package playback

import (
	"context"
	"io"
	"net/http"

	"litepan/internal/domain"
)

func (s *Service) originalLinkHolder(accountID int64, fileID string, res Resolved) *linkHolder {
	return &linkHolder{
		svc:            s,
		link:           res.Link,
		accountID:      accountID,
		fileID:         fileID,
		refreshLeft:    2,
		waitRangeLimit: true,
	}
}

// CopyOriginalRange 将已解析的源文件区间写入 w，并复用播放链路的分片并发、
// 账号并发限制、临时直链刷新和上游传输策略。
func (s *Service) CopyOriginalRange(ctx context.Context, w io.Writer, accountID int64, fileID string, res Resolved, start, end int64) error {
	if s == nil || w == nil || accountID <= 0 || fileID == "" || start < 0 || end < start {
		return domain.Errorf(domain.CodeValidation, "源文件下载参数无效")
	}
	if res.Link.URL == "" {
		return domain.Errorf(domain.CodeDriverError, "无法解析源盘下载地址")
	}
	partSize := res.Link.ChunkSize
	if partSize <= 0 {
		partSize = defaultPartSize
	}
	lh := s.originalLinkHolder(accountID, fileID, res)
	return s.streamUpstreamBody(ctx, w, lh, start, end, partSize)
}

// CopyOriginalFull 用单个完整请求写入源文件，作为未知文件大小或上游不支持
// Range 时的兼容路径。
func (s *Service) CopyOriginalFull(ctx context.Context, w io.Writer, accountID int64, fileID string, res Resolved) error {
	if s == nil || w == nil || accountID <= 0 || fileID == "" || res.Link.URL == "" {
		return domain.Errorf(domain.CodeValidation, "源文件下载参数无效")
	}
	lh := s.originalLinkHolder(accountID, fileID, res)
	link := lh.snapshot()
	for try := 0; try < 3; try++ {
		resp, err := s.doFullRequest(ctx, accountID, link)
		if err != nil {
			fresh, refreshed, refreshErr := lh.refreshAfterFailure(ctx, link)
			if refreshErr != nil {
				return refreshErr
			}
			if refreshed {
				link = fresh
				continue
			}
			return err
		}
		if shouldRefreshUpstreamStatus(resp.StatusCode) {
			resp.Body.Close()
			fresh, refreshed, refreshErr := lh.refreshAfterFailure(ctx, link)
			if refreshErr != nil {
				return refreshErr
			}
			if refreshed {
				link = fresh
				continue
			}
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			resp.Body.Close()
			return domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d", resp.StatusCode)
		}
		_, err = io.Copy(w, resp.Body)
		closeErr := resp.Body.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	return domain.Errorf(domain.CodeDriverError, "源盘下载链接刷新后仍不可用")
}

func (s *Service) doFullRequest(ctx context.Context, accountID int64, link domain.DownloadInfo) (*http.Response, error) {
	release, err := s.rangeLimits.acquireBlocking(ctx, accountID, link.Concurrency)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		release()
		return nil, err
	}
	for key, values := range link.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := s.upstreamClient(link).Do(req)
	if err != nil {
		release()
		return nil, err
	}
	resp.Body = &rangeLimitBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}
