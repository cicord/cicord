package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"time"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/reconcile"
	"github.com/npjonath/cicord/internal/state"
)

// Downloader fetches a CDN URL. When max > 0, a file larger than max bytes is an error
// (ErrTooLarge).
type Downloader func(ctx context.Context, url string, max int64) ([]byte, error)

// ErrTooLarge is returned by a Downloader for a file over its size limit.
var ErrTooLarge = errors.New("file too large")

// HTTPDownloader downloads with an HTTP client (nil: a client with a 30 s timeout).
func HTTPDownloader(c *http.Client) Downloader {
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	return func(ctx context.Context, url string, max int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "DiscordBot (https://github.com/npjonath/cicord, 0)")
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
		}
		var r io.Reader = resp.Body
		if max > 0 {
			r = io.LimitReader(resp.Body, max+1)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", url, err)
		}
		if max > 0 && int64(len(b)) > max {
			return nil, fmt.Errorf("%w (more than %d KiB)", ErrTooLarge, max/1024)
		}
		return b, nil
	}
}

// File is a repository file written by the import (a downloaded image or sound).
type File struct {
	// Name is the reconciler's name of the file: "icon", "banner", "splash",
	// "role:<key>", "emoji:<key>", "sticker:<key>" or "sound:<key>".
	Name string
	// Path is the repository path (slash-separated); Value the YAML value that points
	// to it, relative to servers/<key>.yaml.
	Path  string
	Value string
	Data  []byte
	// URL is the live CDN URL of a server or role image ("" for expressions): it is
	// recorded with the hash of Data, as after an upload by the reconciler.
	URL string
}

// FileMap returns the files by repository path.
func (r *Result) FileMap() map[string][]byte {
	out := make(map[string][]byte, len(r.Files))
	for _, f := range r.Files {
		out[f.Path] = f.Data
	}
	return out
}

// RecordFiles records the downloaded files in the reconciler KV of the server (scope
// "reconcile:<key>") as if the reconciler had uploaded them, so that the first plan
// does not upload them again.
func (r *Result) RecordFiles(ctx context.Context, kv state.KV) error {
	for _, f := range r.Files {
		if err := reconcile.RecordFile(ctx, kv, f.Name, f.Data, f.URL); err != nil {
			return fmt.Errorf("record %s: %w", f.Path, err)
		}
	}
	return nil
}

// Size limits (the ones config validation enforces).
const (
	maxServerImage = 10 << 20
	maxRoleIcon    = 256 << 10
)

func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "gif"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

func sniffSticker(b []byte) string {
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '{' {
		return "json"
	}
	switch f := sniffImage(b); f {
	case "png", "gif":
		return f
	}
	return ""
}

func sniffSound(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("OggS")):
		return "ogg"
	case bytes.HasPrefix(b, []byte("ID3")), len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0:
		return "mp3"
	}
	return ""
}

// download fetches the file of an object into assets/<server key>/<rel>.<ext> (next to
// the server YAML) and returns its YAML value, or "" when it cannot be imported (a
// warning is recorded). what names the object in warnings.
func (g *gen) download(name, url, rel string, max int64, sniff func([]byte) string, what, field string) string {
	if url == "" {
		return ""
	}
	if g.dl == nil {
		g.warn("%s is not imported: download it and set %s to its path", what, field)
		return ""
	}
	data, err := g.dl(g.ctx, url, max)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			g.warn("%s is not imported: the file is larger than the %d KiB Discord accepts for an upload", what, max/1024)
		} else {
			g.warn("%s is not imported: %v (download it and set %s to its path)", what, err, field)
		}
		return ""
	}
	if max > 0 && int64(len(data)) > max {
		g.warn("%s is not imported: the file is larger than the %d KiB Discord accepts for an upload", what, max/1024)
		return ""
	}
	ext := sniff(data)
	if ext == "" {
		g.warn("%s is not imported: %s is not a file type Cicord can upload", what, url)
		return ""
	}
	value := "./" + path.Join("assets", g.res.Key, rel+"."+ext)
	repoPath, ok := config.ResolveRepoPath("servers/"+g.res.Key+".yaml", value)
	if !ok {
		g.warn("%s is not imported: invalid path %s", what, value)
		return ""
	}
	f := File{Name: name, Path: repoPath, Value: value, Data: data}
	if !isExpression(name) {
		f.URL = url
	}
	g.res.Files = append(g.res.Files, f)
	return value
}

func isExpression(name string) bool {
	for _, p := range []string{"emoji:", "sticker:", "sound:"} {
		if len(name) > len(p) && name[:len(p)] == p {
			return true
		}
	}
	return false
}

// importExpressions imports the emojis, stickers and soundboard sounds with their files.
func (g *gen) importExpressions() {
	snap := g.snap
	if g.dl == nil {
		if n := len(snap.Emojis); n > 0 {
			g.warn("%d emoji(s) not imported: export their images to the repository and declare them under spec.emojis", n)
		}
		if n := len(snap.Stickers); n > 0 {
			g.warn("%d sticker(s) not imported: export their files to the repository and declare them under spec.stickers", n)
		}
		if n := len(snap.Sounds); n > 0 {
			g.warn("%d soundboard sound(s) not imported: export their files to the repository and declare them under spec.soundboard", n)
		}
		return
	}
	if len(snap.Emojis) > 0 {
		emojis := append([]discord.Emoji(nil), snap.Emojis...)
		sort.SliceStable(emojis, func(i, j int) bool { return idLess(emojis[i].ID, emojis[j].ID) })
		ns, names := newNamespace(), map[string]bool{}
		for _, e := range emojis {
			label := fmt.Sprintf("emoji :%s: (%s)", e.Name, e.ID)
			switch {
			case e.Managed:
				g.warn("%s is managed by an integration: not imported", label)
				continue
			case names[e.Name]:
				g.warn("%s has the name of another emoji: not imported", label)
				continue
			}
			k := ns.take(Slug(e.Name, "emoji"))
			value := g.download("emoji:"+k, e.ImageURL(), "emojis/"+k, discord.MaxEmojiImageBytes, sniffImage, label, "spec.emojis."+k+".image")
			if value == "" {
				continue
			}
			names[e.Name] = true
			if g.emojis == nil {
				g.emojis = map[string]config.EmojiSpec{}
			}
			g.emojis[k] = config.EmojiSpec{Name: e.Name, Image: value, Roles: g.keysOf(e.RoleIDs, g.roleKeys, "role", label)}
			g.emojiOrder = append(g.emojiOrder, k)
			g.record(state.KindEmoji, k, e.ID)
		}
	}
	if len(snap.Stickers) > 0 {
		stickers := append([]discord.Sticker(nil), snap.Stickers...)
		sort.SliceStable(stickers, func(i, j int) bool { return idLess(stickers[i].ID, stickers[j].ID) })
		ns := newNamespace()
		for _, s := range stickers {
			label := fmt.Sprintf("sticker %q (%s)", s.Name, s.ID)
			k := ns.take(Slug(s.Name, "sticker"))
			value := g.download("sticker:"+k, s.FileURL(), "stickers/"+k, discord.MaxStickerFileBytes, sniffSticker, label, "spec.stickers."+k+".file")
			if value == "" {
				continue
			}
			if g.stickers == nil {
				g.stickers = map[string]config.StickerSpec{}
			}
			g.stickers[k] = config.StickerSpec{Name: s.Name, Description: s.Description, Emoji: s.Tags, File: value}
			g.stickerOrder = append(g.stickerOrder, k)
			g.record(state.KindSticker, k, s.ID)
		}
	}
	if len(snap.Sounds) > 0 {
		sounds := append([]discord.SoundboardSound(nil), snap.Sounds...)
		sort.SliceStable(sounds, func(i, j int) bool { return idLess(sounds[i].ID, sounds[j].ID) })
		ns := newNamespace()
		for _, s := range sounds {
			label := fmt.Sprintf("soundboard sound %q (%s)", s.Name, s.ID)
			k := ns.take(Slug(s.Name, "sound"))
			value := g.download("sound:"+k, s.FileURL(), "sounds/"+k, discord.MaxSoundFileBytes, sniffSound, label, "spec.soundboard."+k+".sound")
			if value == "" {
				continue
			}
			spec := config.SoundboardSpec{Name: s.Name, Sound: value, Emoji: s.Emoji}
			if v := s.Volume; v < 0.995 {
				spec.Volume = &v
			}
			if g.sounds == nil {
				g.sounds = map[string]config.SoundboardSpec{}
			}
			g.sounds[k] = spec
			g.soundOrder = append(g.soundOrder, k)
			g.record(state.KindSound, k, s.ID)
		}
	}
}
