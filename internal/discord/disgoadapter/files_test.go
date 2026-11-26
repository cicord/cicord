package disgoadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

func TestUploadsAcrossDiscordEndpoints(t *testing.T) {
	f := newFakeDiscord(t)
	s := f.connect(t)
	ctx := context.Background()
	payload := discord.MessageCreate{Files: []discord.UploadFile{{Name: "report.md", Data: []byte{0, 255, 'A'}, ContentType: "text/markdown", Description: "transcript"}}, Embeds: []discord.Embed{{ImageURL: "attachment://report.md"}}}
	for _, tc := range []struct {
		name, path string
		callback   bool
		do         func() error
	}{
		{"send", "POST /channels/123/messages", false, func() error { _, e := s.SendMessage(ctx, "123", payload); return e }},
		{"edit", "PATCH /channels/123/messages/456", false, func() error { _, e := s.EditMessage(ctx, "123", "456", payload); return e }},
		{"reply", "POST /interactions/456/token/callback", true, func() error {
			return s.RespondInteraction(ctx, "456", "token", discord.InteractionResponse{Type: discord.ResponseMessage, Data: &payload})
		}},
		{"update", "POST /interactions/456/token/callback", true, func() error {
			return s.RespondInteraction(ctx, "456", "token", discord.InteractionResponse{Type: discord.ResponseUpdateMessage, Data: &payload})
		}},
		{"deferred edit", "PATCH /webhooks/601/token/messages/@original", false, func() error { _, e := s.EditOriginalResponse(ctx, "601", "token", payload); return e }},
		{"followup", "POST /webhooks/601/token", false, func() error { _, e := s.CreateFollowup(ctx, "601", "token", payload); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.json(tc.path, 200, "{}")
			if err := tc.do(); err != nil {
				t.Fatal(err)
			}
			rec := f.last()
			ct, params, err := mime.ParseMediaType(rec.ContentType)
			if err != nil || ct != "multipart/form-data" {
				t.Fatal(rec.ContentType)
			}
			r := multipart.NewReader(bytes.NewReader(rec.Raw), params["boundary"])
			p, err := r.NextPart()
			if err != nil || p.FormName() != "payload_json" {
				t.Fatal(err)
			}
			var data map[string]any
			if err = json.NewDecoder(p).Decode(&data); err != nil {
				t.Fatal(err)
			}
			if tc.callback {
				data = data["data"].(map[string]any)
			}
			a := data["attachments"].([]any)[0].(map[string]any)
			if a["id"] != float64(0) || a["description"] != "transcript" || a["filename"] != "report.md" {
				t.Fatal(a)
			}
			part, err := r.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(part)
			if part.FormName() != "files[0]" || part.FileName() != "report.md" || part.Header.Get("Content-Type") != "text/markdown" || !bytes.Equal(b, payload.Files[0].Data) {
				t.Fatal("incorrect file part")
			}
		})
	}
}
