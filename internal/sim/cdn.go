package sim

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cicord/cicord/internal/discord"
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
	w.mu.Lock()
	defer w.mu.Unlock()
	b, ok := w.cdn[strings.SplitN(url, "?", 2)[0]]
	if !ok {
		return nil, fmt.Errorf("GET %s: HTTP %d: %w", url, http.StatusNotFound, discord.ErrNotFound)
	}
	if max > 0 && int64(len(b)) > max {
		return nil, fmt.Errorf("GET %s: the file is larger than %d KiB", url, max/1024)
	}
	return bytes.Clone(b), nil
}

func (w *World) uploads(channel string, files []discord.UploadFile) []discord.Attachment {
	var out []discord.Attachment
	for _, f := range files {
		id := w.newID()
		loc := discord.CDNBase + "/attachments/" + channel + "/" + id + "/" + url.PathEscape(f.Name)
		w.cdn[loc] = bytes.Clone(f.Data)
		ct := f.ContentType
		if ct == "" {
			ct = http.DetectContentType(f.Data)
		}
		out = append(out, discord.Attachment{ID: id, Filename: f.Name, URL: loc, Size: len(f.Data), ContentType: ct, Description: f.Description})
	}
	return out
}
func (w *World) clearUploads(a []discord.Attachment) {
	for _, f := range a {
		delete(w.cdn, f.URL)
	}
}
func (c *Client) DownloadCDN(ctx context.Context, url string, max int64) ([]byte, error) {
	return c.w.Download(ctx, url, max)
}
