package disgoadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"

	"github.com/cicord/cicord/internal/discord"
	disgo "github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

type uploadMetadata struct {
	ID          int    `json:"id"`
	Filename    string `json:"filename"`
	Description string `json:"description,omitempty"`
}

func uploadDescriptions(files []discord.UploadFile) []uploadMetadata {
	out := make([]uploadMetadata, len(files))
	for n, f := range files {
		out[n] = uploadMetadata{n, f.Name, f.Description}
	}
	return out
}

func (s *session) doFiles(ctx context.Context, op string, ep *rest.CompiledEndpoint, body, out any, files []discord.UploadFile) error {
	if len(files) == 0 {
		return s.do(ctx, op, ep, body, out)
	}
	if err := discord.ValidateUploads(files, discord.UploadLimit(3)); err != nil {
		return err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	payload, err := w.CreateFormField("payload_json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(payload).Encode(body); err != nil {
		return err
	}
	for idx, f := range files {
		t := f.ContentType
		if t == "" {
			t = "application/octet-stream"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", t)
		h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": fmt.Sprintf("files[%d]", idx), "filename": f.Name}))
		p, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := p.Write(f.Data); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return s.do(ctx, op, ep, &disgo.MultipartBuffer{Buffer: &buf, ContentType: w.FormDataContentType()}, out)
}
