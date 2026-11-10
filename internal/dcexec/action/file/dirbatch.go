//go:build !windows

package file

import (
	"errors"
	"io"
	"os"
)

const (
	// dirReadBatchSize is how many directory entries each Readdirnames call fills.
	dirReadBatchSize = 256
	// listOverflowSlack is extra names a listing may return beyond the caller's window.
	// Callers pass the settings window (file.listMaxEntries); the listing returns up to
	// that many names plus this slack so truncated can be reported without another round trip.
	listOverflowSlack = 100
)

// listReadCap is how many names a directory or trash listing may return.
// The extra names are part of the result. readDirNames reports truncated when
// the directory still continues after this cap.
func listReadCap(limit int) int {
	return limit + listOverflowSlack
}

// readDirNames reads up to limit names from dir using batched Readdirnames calls.
// limit must be > 0 for bounded listing from FILE_LIST / TRASH_LIST. truncated is
// true when limit was reached and at least one more name remained unread.
func readDirNames(dir string, limit int) (names []string, truncated bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	for {
		n := dirReadBatchSize
		if limit > 0 {
			remain := limit - len(names)
			if remain <= 0 {
				break
			}
			n = min(n, remain)
		}

		chunk, rdErr := f.Readdirnames(n)
		if len(chunk) > 0 {
			names = append(names, chunk...)
		}
		if rdErr != nil {
			if errors.Is(rdErr, io.EOF) {
				return names, false, nil
			}
			return nil, false, rdErr
		}
		if len(chunk) < n {
			return names, false, nil
		}
		if limit > 0 && len(names) >= limit {
			extra, extraErr := f.Readdirnames(1)
			if extraErr != nil && !errors.Is(extraErr, io.EOF) {
				return nil, false, extraErr
			}
			return names, len(extra) > 0, nil
		}
	}
	return names, false, nil
}

// forEachDirBatch visits every name in dir, one Readdirnames batch at a time,
// without retaining the whole directory.
func forEachDirBatch(dir string, fn func(names []string) error) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	for {
		chunk, rdErr := f.Readdirnames(dirReadBatchSize)
		if len(chunk) > 0 {
			if err := fn(chunk); err != nil {
				return err
			}
		}
		if rdErr != nil {
			if errors.Is(rdErr, io.EOF) {
				return nil
			}
			return rdErr
		}
		if len(chunk) < dirReadBatchSize {
			return nil
		}
	}
}
