package importer

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/reconcile"
	"github.com/npjonath/cicord/internal/sim"
	"github.com/npjonath/cicord/internal/state"
	"github.com/npjonath/cicord/internal/state/sqlite"
)

func testPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func testGIF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	frames := &gif.GIF{}
	for i := 0; i < 2; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 16, 16), palette.Plan9)
		img.SetColorIndex(i, i, uint8(10+i))
		frames.Image = append(frames.Image, img)
		frames.Delay = append(frames.Delay, 10)
	}
	if err := gif.EncodeAll(&b, frames); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// assetServer is a simulated server with every kind of file: server icon, banner and
// invite splash, a role icon, a static and an animated emoji, a sticker and a sound.
func assetServer(t *testing.T) (*sim.World, discord.Server, string, discord.User) {
	t.Helper()
	ctx := context.Background()
	w := sim.NewWorld()
	owner := w.CreateUser("owner")
	srv := w.CreateServer("Assets", owner.ID)
	token, bot, _ := w.CreateBot("cicord")
	if _, err := w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator")); err != nil {
		t.Fatal(err)
	}
	if err := w.SetBoosts(srv.ID, 14); err != nil {
		t.Fatal(err)
	}
	cl := w.AdminClient(srv.ID)
	icon, banner, splash := testPNG(t, 64, 64, color.RGBA{1, 2, 3, 255}), testPNG(t, 96, 54, color.RGBA{4, 5, 6, 255}), testPNG(t, 96, 54, color.RGBA{7, 8, 9, 255})
	if _, err := cl.UpdateServer(ctx, srv.ID, discord.ServerParams{Icon: &icon, Banner: &banner, Splash: &splash}); err != nil {
		t.Fatal(err)
	}
	name, roleIcon := "Star", testPNG(t, 32, 32, color.RGBA{200, 200, 0, 255})
	star, err := cl.CreateRole(ctx, srv.ID, discord.RoleParams{Name: &name}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.UpdateRole(ctx, srv.ID, star.ID, discord.RoleParams{Icon: &roleIcon}, ""); err != nil {
		t.Fatal(err)
	}
	roles := []string{star.ID}
	if _, err := cl.CreateEmoji(ctx, srv.ID, discord.EmojiParams{Name: "party", Image: testPNG(t, 32, 32, color.RGBA{9, 9, 9, 255}), RoleIDs: &roles}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.CreateEmoji(ctx, srv.ID, discord.EmojiParams{Name: "dance", Image: testGIF(t)}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.CreateSticker(ctx, srv.ID, discord.StickerParams{Name: "Wave", Description: "Waving hand", Tags: "wave", File: testPNG(t, 160, 160, color.RGBA{3, 3, 3, 255})}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.CreateSoundboardSound(ctx, srv.ID, discord.SoundParams{Name: "Horn", Volume: 0.5, Emoji: "📯", Sound: append([]byte("OggS\x00\x02"), make([]byte, 64)...)}, ""); err != nil {
		t.Fatal(err)
	}
	return w, srv, token, bot
}

// importInto generates the server, writes the YAML and the files into a new repository,
// records the resource map and the file states, and returns the repository and store.
func importInto(t *testing.T, w *sim.World, srv discord.Server, token string, dl Downloader) (*Result, *config.Repo, state.Store, discord.Session) {
	t.Helper()
	ctx := context.Background()
	sess, err := w.Connect(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reconcile.FetchWith(ctx, sess, srv.ID, "", reconcile.FetchAll)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Generate(ctx, Options{ServerKey: "assets", Snapshot: snap, Download: dl})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cicord.yaml", []byte("apiVersion: cicord.dev/v1alpha1\nkind: Project\nmetadata:\n  name: p\nspec:\n  engine: \">=0.0.0\"\n"))
	write("servers/assets.yaml", res.YAML)
	for p, data := range res.FileMap() {
		write(p, data)
	}
	repo, diags := config.Load(dir, config.LoadOptions{})
	if repo == nil || diags.HasErrors() {
		t.Fatalf("imported YAML does not validate: %v\n%s", diags.Errors(), res.YAML)
	}
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for _, r := range res.Resources {
		if err := store.Resources().Put(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := res.RecordFiles(ctx, store.KV("reconcile:assets")); err != nil {
		t.Fatal(err)
	}
	return res, repo, store, sess
}

func planOf(t *testing.T, repo *config.Repo, store state.Store, sess discord.Session) reconcile.Plan {
	t.Helper()
	rec := reconcile.New(reconcile.Options{Server: repo.Servers["assets"], RepoRoot: repo.Root, Client: sess,
		Resources: store.Resources(), KV: store.KV("reconcile:assets"), BotUserID: sess.Self().ID})
	defer rec.Close()
	p, err := rec.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestImportAssetsRoundTrip: the files of a server are downloaded into the repository,
// referenced from the YAML, and the plan right after the import is empty.
func TestImportAssetsRoundTrip(t *testing.T) {
	w, srv, token, _ := assetServer(t)
	res, repo, store, sess := importInto(t, w, srv, token, w.Download)
	for _, want := range []string{
		"icon: ./assets/assets/icon.png", "banner: ./assets/assets/banner.png", "splash: ./assets/assets/splash.png",
		"icon: ./assets/assets/roles/star.png",
		"image: ./assets/assets/emojis/party.png", "image: ./assets/assets/emojis/dance.gif", "roles:\n        - star",
		"file: ./assets/assets/stickers/wave.png", "emoji: wave", "description: Waving hand",
		"sound: ./assets/assets/sounds/horn.ogg", "volume: 0.5",
	} {
		if !strings.Contains(string(res.YAML), want) {
			t.Errorf("YAML misses %q:\n%s", want, res.YAML)
		}
	}
	for _, w := range res.Warnings {
		if !strings.Contains(w, "managed by an integration or bot") { // the bot's own role
			t.Errorf("unexpected warning %q", w)
		}
	}
	paths := res.FileMap()
	for _, p := range []string{"servers/assets/assets/icon.png", "servers/assets/assets/emojis/dance.gif", "servers/assets/assets/sounds/horn.ogg"} {
		if len(paths[p]) == 0 {
			t.Errorf("%s not downloaded: %v", p, paths)
		}
	}
	if !bytes.HasPrefix(paths["servers/assets/assets/emojis/dance.gif"], []byte("GIF8")) {
		t.Error("the animated emoji is not a GIF")
	}
	counts := map[state.ResourceKind]int{}
	for _, r := range res.Resources {
		counts[r.Kind]++
	}
	if counts[state.KindEmoji] != 2 || counts[state.KindSticker] != 1 || counts[state.KindSound] != 1 {
		t.Errorf("resources = %v", counts)
	}
	if p := planOf(t, repo, store, sess); !p.Empty() || len(p.Blocked) != 0 {
		t.Fatalf("plan after import is not empty:\n%s\n%s", p, res.YAML)
	}
	// The recorded hashes make a later file change visible (an expression file cannot be
	// replaced: blocked).
	if err := os.WriteFile(filepath.Join(repo.Root, "servers/assets/assets/emojis/party.png"), testPNG(t, 32, 32, color.RGBA{1, 1, 1, 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := planOf(t, repo, store, sess); len(p.Blocked) != 1 || p.Blocked[0].Key != "party" {
		t.Fatalf("changed emoji file not detected:\n%s", p)
	}
}

// TestImportAssetsLimitsAndFailures: a file over Discord's upload limit or a failed
// download is not referenced (a warning says why); the rest is imported and planned empty.
func TestImportAssetsLimitsAndFailures(t *testing.T) {
	w, srv, token, _ := assetServer(t)
	dl := func(ctx context.Context, url string, max int64) ([]byte, error) {
		switch {
		case strings.Contains(url, "/banners/"):
			return bytes.Repeat([]byte{0x89}, int(max)+1), nil // misbehaving downloader: checked again
		case strings.Contains(url, "/stickers/"):
			return nil, ErrTooLarge
		case strings.Contains(url, "/soundboard-sounds/"):
			return nil, errors.New("connection reset")
		}
		return w.Download(ctx, url, max)
	}
	res, repo, store, sess := importInto(t, w, srv, token, dl)
	y := string(res.YAML)
	if strings.Contains(y, "banner:") || strings.Contains(y, "stickers:") || strings.Contains(y, "soundboard:") || !strings.Contains(y, "emojis:") {
		t.Errorf("YAML:\n%s", y)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"the server banner is not imported: the file is larger than", `sticker "Wave"`, "connection reset"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings miss %q: %v", want, res.Warnings)
		}
	}
	if p := planOf(t, repo, store, sess); !p.Empty() || len(p.Blocked) != 0 {
		t.Fatalf("plan after import is not empty:\n%s", p)
	}
}

// Without a downloader, the files are reported as not imported (previous behaviour).
func TestImportAssetsWithoutDownloader(t *testing.T) {
	w, srv, token, _ := assetServer(t)
	res, _, _, _ := importInto(t, w, srv, token, nil)
	if len(res.Files) != 0 || strings.Contains(string(res.YAML), "emojis:") {
		t.Errorf("files = %v", res.Files)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"the server icon is not imported", "2 emoji(s) not imported", "1 sticker(s)", "1 soundboard sound(s)", "roles.star.icon"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings miss %q: %v", want, res.Warnings)
		}
	}
}

func TestHTTPDownloader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("hello"))
		case "/big":
			w.Write(bytes.Repeat([]byte("x"), 2048))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	dl := HTTPDownloader(ts.Client())
	ctx := context.Background()
	if b, err := dl(ctx, ts.URL+"/ok", 1024); err != nil || string(b) != "hello" {
		t.Errorf("ok = %q %v", b, err)
	}
	if _, err := dl(ctx, ts.URL+"/big", 1024); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big = %v", err)
	}
	if b, err := dl(ctx, ts.URL+"/big", 0); err != nil || len(b) != 2048 {
		t.Errorf("unbounded = %d %v", len(b), err)
	}
	if _, err := dl(ctx, ts.URL+"/missing", 1024); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing = %v", err)
	}
}
