package sim

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/npjonath/codi/internal/discord"
)

// cdnPut keeps the bytes served at a CDN URL and returns the URL. Caller holds w.mu.
func (w *World) cdnPut(url string, b []byte) string {
	if url != "" && len(b) > 0 {
		w.cdn[url] = append([]byte(nil), b...)
	}
	return url
}

// CDNFile returns the file served at a simulated CDN URL: server and role images,
// emoji images, sticker and sound files, as uploaded.
func (w *World) CDNFile(url string) ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, ok := w.cdn[strings.SplitN(url, "?", 2)[0]]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// Download fetches a simulated CDN URL (the signature of importer.Downloader): an
// unknown URL is a 404, a file larger than max (when max > 0) is an error.
func (w *World) Download(ctx context.Context, url string, max int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := w.CDNFile(url)
	if !ok {
		return nil, fmt.Errorf("GET %s: HTTP %d: %w", url, http.StatusNotFound, discord.ErrNotFound)
	}
	if max > 0 && int64(len(b)) > max {
		return nil, fmt.Errorf("GET %s: the file is larger than %d KiB", url, max/1024)
	}
	return b, nil
}
