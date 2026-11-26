package sim

import (
	"bytes"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestAttachmentLifecycle(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	file := discord.UploadFile{Name: "a b.png", Data: []byte{0, 255, 1}, Description: "test", ContentType: "image/png"}
	m, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Files: []discord.UploadFile{file}})
	if err != nil {
		t.Fatal(err)
	}
	a := m.Attachments[0]
	b, err := f.sess.DownloadCDN(ctx, a.URL, 3)
	if err != nil || a.Description != "test" || a.Size != 3 || !bytes.Equal(file.Data, b) {
		t.Fatal(a, b, err)
	}
	if _, err := f.sess.DownloadCDN(ctx, a.URL, 2); err == nil {
		t.Fatal("oversized download")
	}
	b[0]++
	if file.Data[0] != 0 {
		t.Fatal("shared mutable file")
	}
	_, err = f.sess.EditMessage(ctx, f.general.ID, m.ID, discord.MessageCreate{Content: "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.w.CDNFile(a.URL); !ok {
		t.Fatal("edit removed existing attachment")
	}
	_, err = f.sess.EditMessage(ctx, f.general.ID, m.ID, discord.MessageCreate{Content: "removed", Files: []discord.UploadFile{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.w.CDNFile(a.URL); ok {
		t.Fatal("cleared attachment still available")
	}
	if _, err := f.sess.SendMessage(ctx, f.general.ID, discord.MessageCreate{Files: []discord.UploadFile{file, file}}); err == nil {
		t.Fatal("duplicate names accepted")
	}
}
