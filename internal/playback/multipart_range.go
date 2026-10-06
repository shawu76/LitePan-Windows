package playback

import (
	"mime/multipart"
	"net/http"
	"net/textproto"
)

func (r byteRange) mimeHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Type":  {contentType},
		"Content-Range": {r.contentRange(size)},
	}
}

type rangeByteCounter int64

func (c *rangeByteCounter) Write(p []byte) (int, error) {
	*c += rangeByteCounter(len(p))
	return len(p), nil
}

func (s *Service) streamMultipartRanges(w http.ResponseWriter, r *http.Request, lh *linkHolder, ranges []byteRange, size, partSize int64, headers streamHeaders) error {
	mw := multipart.NewWriter(w)
	// 只计算 MIME 头和边界长度，不预读正文；实际数据仍走原有分片流式代理。
	var length rangeByteCounter
	counter := multipart.NewWriter(&length)
	if err := counter.SetBoundary(mw.Boundary()); err != nil {
		return err
	}
	for _, ra := range ranges {
		if _, err := counter.CreatePart(ra.mimeHeader(headers.contentType, size)); err != nil {
			return err
		}
		length += rangeByteCounter(ra.length())
	}
	if err := counter.Close(); err != nil {
		return err
	}
	contentType := headers.contentType
	headers.contentType = "multipart/byteranges; boundary=" + mw.Boundary()
	headers.contentLength = int64(length)
	writeStreamHeaders(w, headers)
	w.WriteHeader(http.StatusPartialContent)
	for _, ra := range ranges {
		part, err := mw.CreatePart(ra.mimeHeader(contentType, size))
		if err != nil {
			return err
		}
		if err := s.streamUpstreamBody(r.Context(), part, lh, ra.start, ra.end, partSize); err != nil {
			return err
		}
	}
	return mw.Close()
}
