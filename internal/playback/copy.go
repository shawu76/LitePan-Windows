package playback

import (
	"context"
	"io"
	"net/http"
	"time"

	"litepan/internal/domain"
)

func (s *Service) originalLinkHolder(accountID int64, fileID string, res Resolved) *linkHolder {
	return &linkHolder{
		svc:         s,
		link:        res.Link,
		accountID:   accountID,
		fileID:      fileID,
		refreshLeft: transferRefreshes,
	}
}

// CopyOriginalFull 用单个完整请求写入源文件，作为未知文件大小或上游不支持
// Range 时的兼容路径。
func (s *Service) CopyOriginalFull(ctx context.Context, w io.Writer, accountID int64, fileID string, res Resolved) error {
	if s == nil || w == nil || accountID <= 0 || fileID == "" || res.Link.URL == "" {
		return domain.Errorf(domain.CodeValidation, "源文件下载参数无效")
	}
	lh := s.originalLinkHolder(accountID, fileID, res)
	link := lh.snapshot()
	var lastErr error
	for try := 0; try < transferRetries; try++ {
		if try > 0 {
			if err := waitTransferRetry(ctx, (1<<min(try-1, 3))*500*time.Millisecond); err != nil {
				return err
			}
		}
		resp, err := s.doFullRequest(ctx, accountID, link)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			fresh, refreshed, refreshErr := lh.refreshAfterFailure(ctx, link, resp.StatusCode)
			if refreshErr != nil {
				return refreshErr
			}
			if refreshed {
				link = fresh
				try--
				continue
			}
			return domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d，换链次数已用尽", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= http.StatusInternalServerError {
			if resp.StatusCode == http.StatusTooManyRequests {
				s.transferLimits.coolDown(accountID, transferRetryAfter(resp.Header.Get("Retry-After")))
			}
			resp.Body.Close()
			lastErr = domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d", resp.StatusCode)
			continue
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
	return lastErr
}

func (s *Service) doFullRequest(ctx context.Context, accountID int64, link domain.DownloadInfo) (*http.Response, error) {
	_, limit := transferSettings(link)
	release, err := s.transferLimits.acquireBlocking(ctx, accountID, limit)
	if err != nil {
		return nil, err
	}
	if err := s.transferLimits.waitCoolDown(ctx, accountID); err != nil {
		release()
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
