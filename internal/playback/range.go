package playback

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var errRangeNoOverlap = errors.New("range outside file")

type byteRange struct {
	start, end int64
}

func (r byteRange) length() int64 { return r.end - r.start + 1 }

func (r byteRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.end, size)
}

func parseRanges(header string, size int64) ([]byteRange, error) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), "bytes=") {
		return nil, fmt.Errorf("invalid range")
	}
	// 过多分段或累计长度大于文件时忽略 Range，避免小请求放大上游读取。
	if strings.Count(header, ",") >= 32 {
		return nil, nil
	}
	var ranges []byteRange
	var total int64
	for spec := range strings.SplitSeq(header[6:], ",") {
		if strings.TrimSpace(spec) == "" {
			continue
		}
		start, end, err := parseSingleRange("bytes="+spec, size)
		if errors.Is(err, errRangeNoOverlap) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ra := byteRange{start: start, end: end}
		if ra.length() > size-total {
			return nil, nil
		}
		total += ra.length()
		ranges = append(ranges, ra)
	}
	if len(ranges) == 0 {
		return nil, errRangeNoOverlap
	}
	return ranges, nil
}

func parseSingleRange(header string, size int64) (start, end int64, err error) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), "bytes=") {
		return 0, 0, fmt.Errorf("invalid range")
	}
	spec := strings.TrimSpace(header[6:])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, fmt.Errorf("multipart range not supported")
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range")
	}
	parts[0], parts[1] = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if parts[0] == "" {
		// suffix: bytes=-500
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, fmt.Errorf("invalid suffix range")
		}
		if size <= 0 {
			return 0, 0, fmt.Errorf("unknown size")
		}
		start = size - suffix
		if start < 0 {
			start = 0
		}
		return start, size - 1, nil
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("invalid range start")
	}
	if parts[1] == "" {
		if size <= 0 {
			return start, start, nil
		}
		if start >= size {
			return 0, 0, errRangeNoOverlap
		}
		return start, size - 1, nil
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, fmt.Errorf("invalid range end")
	}
	if size > 0 && start >= size {
		return 0, 0, errRangeNoOverlap
	}
	if size > 0 && end >= size {
		end = size - 1
	}
	return start, end, nil
}
