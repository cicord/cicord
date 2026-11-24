package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cicord/cicord/internal/writeback"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	if err := gif.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSniffImage(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		format string
		w, h   int
		err    string
	}{
		{"png", pngBytes(t, 640, 320), "png", 640, 320, ""},
		{"gif", gifBytes(t, 32, 16), "gif", 32, 16, ""},
		// VP8L: signature 0x2f, 14-bit (w-1) and (h-1).
		{"webp lossless", append([]byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f"), 0xff, 0xc1, 0x3f, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0), "webp", 512, 256, ""},
		{"webp extended", append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), 0xff, 0x03, 0x00, 0xff, 0x01, 0x00, 0, 0, 0, 0), "webp", 1024, 512, ""},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "", 0, 0, "unsupported"},
		{"truncated png", []byte("\x89PNG\r\n\x1a\n\x00\x00"), "", 0, 0, "corrupt"},
	}
	for _, c := range cases {
		f, w, h, err := sniffImage(c.data)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || f != c.format || w != c.w || h != c.h {
			t.Errorf("%s: got %s %dx%d %v, want %s %dx%d", c.name, f, w, h, err, c.format, c.w, c.h)
		}
	}
}

func TestAssetUpload(t *testing.T) {
	e := start(t)
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	base := "/api/v1/servers/" + serverKey + "/assets/"
	img := pngBytes(t, 512, 512)
	rev1, _ := e.rt.Revision()

	// A moderator cannot upload.
	mod := e.client()
	mod.login(e.moderator().ID)
	if r := mod.do("PUT", base+"icon", img, "Content-Type", "image/png"); r.status != 403 {
		t.Fatalf("moderator upload = %d %s", r.status, r.body)
	}
	// Unknown kind, wrong type, too large, GIF splash, stale revision.
	if r := owner.do("PUT", base+"avatar", img, "Content-Type", "image/png"); r.status != 404 {
		t.Fatalf("unknown kind = %d %s", r.status, r.body)
	}
	if r := owner.do("PUT", base+"icon", []byte("<svg></svg>"), "Content-Type", "image/svg+xml"); r.status != 415 {
		t.Fatalf("svg = %d %s", r.status, r.body)
	}
	if r := owner.do("PUT", base+"banner", make([]byte, maxAssetBytes+1), "Content-Type", "image/png"); r.status != 413 {
		t.Fatalf("too large = %d %s", r.status, r.body)
	}
	if r := owner.do("PUT", base+"splash", gifBytes(t, 1920, 1080), "Content-Type", "image/gif"); r.status != 415 {
		t.Fatalf("gif splash = %d %s", r.status, r.body)
	}
	if r := owner.do("PUT", base+"icon?revision=stale", img, "Content-Type", "image/png"); r.status != 409 {
		t.Fatalf("stale revision = %d %s", r.status, r.body)
	}

	// Happy path: file written, YAML patched, new revision active.
	var out assetPutResponse
	r := owner.do("PUT", base+"icon?revision="+rev1.ID, img, "Content-Type", "image/png")
	if r.status != 200 {
		t.Fatalf("upload = %d %s", r.status, r.body)
	}
	r.json(t, &out)
	if out.Mode != "commit" || out.Path != "servers/assets/ma-commu-icon.png" || out.Value != "./assets/ma-commu-icon.png" ||
		out.Format != "png" || out.Width != 512 || len(out.Warnings) != 0 {
		t.Fatalf("response = %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(e.dir, "servers/assets/ma-commu-icon.png"))
	if err != nil || !bytes.Equal(data, img) {
		t.Fatalf("asset file: %v", err)
	}
	yml, _ := os.ReadFile(filepath.Join(e.dir, "servers/ma-commu.yaml"))
	if !strings.Contains(string(yml), "icon: ./assets/ma-commu-icon.png") || !strings.Contains(string(yml), "systemChannel: general") {
		t.Fatalf("server YAML not patched:\n%s", yml)
	}
	rev2, _ := e.rt.Revision()
	if rev2.SHA == rev1.SHA || out.Revision != rev2.ID {
		t.Fatalf("new revision not active: %s → %s (response %q)", rev1.ID, rev2.ID, out.Revision)
	}
	srv, _ := e.rt.ServerConfig(serverKey)
	if srv.Spec.Profile.Icon != "./assets/ma-commu-icon.png" {
		t.Fatalf("active profile.icon = %q", srv.Spec.Profile.Icon)
	}

	// Preview of the declared file (moderators may read it).
	got := mod.do("GET", base+"icon", nil)
	if got.status != 200 || !bytes.Equal(got.body, img) || got.header.Get("Content-Type") != "image/png" {
		t.Fatalf("GET asset = %d %s", got.status, got.header)
	}
	if r := mod.do("GET", base+"banner", nil); r.status != 404 {
		t.Fatalf("GET undeclared banner = %d", r.status)
	}

	// Same image again: no-op.
	owner.expect(200, "PUT", base+"icon", img).json(t, &out)
	if rev3, _ := e.rt.Revision(); rev3.SHA != rev2.SHA {
		t.Fatalf("no-op upload created a revision")
	}

	// A small banner is accepted with a warning.
	owner.expect(200, "PUT", base+"banner", pngBytes(t, 300, 100)).json(t, &out)
	if len(out.Warnings) == 0 || out.Value != "./assets/ma-commu-banner.png" {
		t.Fatalf("banner = %+v", out)
	}

	var audit struct{ Items []auditDTO }
	owner.expect(200, "GET", "/api/v1/servers/"+serverKey+"/audit", nil).json(t, &audit)
	if len(audit.Items) == 0 || audit.Items[0].Action != "server.asset" || audit.Items[0].Target != serverKey+"/banner" {
		t.Fatalf("audit = %+v", audit.Items)
	}
}

// prWriter records requests and answers like a pull-request write-back.
type prWriter struct {
	mu   sync.Mutex
	reqs []writeback.Request
}

func (p *prWriter) Write(_ context.Context, req writeback.Request) (writeback.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	return writeback.Result{Mode: writeback.ModePullRequest, PullRequestURL: "https://github.com/example/ma-commu-cicord/pull/7"}, nil
}

func TestAssetUploadPullRequest(t *testing.T) {
	pw := &prWriter{}
	e := startWith(t, func(o *Options) { o.Writer = pw })
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	var out assetPutResponse
	owner.expect(200, "PUT", "/api/v1/servers/"+serverKey+"/assets/banner", gifBytes(t, 960, 540)).json(t, &out)
	if out.Mode != "pullRequest" || out.Revision != "" || !strings.HasSuffix(out.PullRequestURL, "/pull/7") || out.Format != "gif" {
		t.Fatalf("response = %+v", out)
	}
	if len(pw.reqs) != 1 {
		t.Fatalf("writes = %d", len(pw.reqs))
	}
	req := pw.reqs[0]
	// cicord.yaml: servers/** → pullRequest.
	if req.Mode != writeback.ModePullRequest || len(req.Edits) != 2 || !strings.HasPrefix(req.Message, "server(ma-commu): update banner") {
		t.Fatalf("request = %+v", req)
	}
	yml, _ := os.ReadFile(filepath.Join(e.dir, "servers/ma-commu.yaml"))
	next, err := req.Edits["servers/ma-commu.yaml"](yml)
	if err != nil || !strings.Contains(string(next), "banner: ./assets/ma-commu-banner.gif") {
		t.Fatalf("yaml edit: %v\n%s", err, next)
	}
	if bin, _ := req.Edits["servers/assets/ma-commu-banner.gif"](nil); !bytes.HasPrefix(bin, []byte("GIF89a")) {
		t.Fatal("binary edit does not return the GIF unchanged")
	}
}

func TestStatusLiveServer(t *testing.T) {
	e := start(t)
	if err := e.world.SetBoosts(e.serverID(), 7); err != nil {
		t.Fatal(err)
	}
	owner := e.client()
	owner.login(e.seed.Owner.ID)
	var st struct {
		Servers []struct {
			Key        string   `json:"key"`
			BoostLevel int      `json:"boostLevel"`
			BoostCount int      `json:"boostCount"`
			Features   []string `json:"features"`
			Community  bool     `json:"community"`
			IconURL    *string  `json:"iconUrl"`
		} `json:"servers"`
	}
	e.waitFor("boost level in status", func() bool {
		owner.expect(200, "GET", "/api/v1/status", nil).json(t, &st)
		return len(st.Servers) == 1 && st.Servers[0].BoostLevel == 2
	})
	s := st.Servers[0]
	if s.BoostCount != 7 || s.IconURL == nil || s.Features == nil {
		t.Fatalf("status server = %+v", s)
	}
	var detail struct {
		BoostLevel int      `json:"boostLevel"`
		Features   []string `json:"features"`
	}
	owner.expect(200, "GET", "/api/v1/servers/"+serverKey, nil).json(t, &detail)
	if detail.BoostLevel != 2 || !strings.Contains(strings.Join(detail.Features, ","), "BANNER") {
		t.Fatalf("server detail = %+v", detail)
	}
}
