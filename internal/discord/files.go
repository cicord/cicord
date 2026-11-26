package discord

import (
	"fmt"
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"
)

type UploadFile struct {
	Name        string `json:"name"`
	Data        []byte `json:"data"`
	ContentType string `json:"contentType,omitempty"`
	Description string `json:"description,omitempty"`
}

// UploadLimit is the per-file guild boost limit. The sandbox separately bounds total in-flight bytes.
func UploadLimit(tier int) int {
	if tier >= 3 {
		return 100 << 20
	}
	if tier == 2 {
		return 50 << 20
	}
	return 10 << 20
}

func ValidateUploads(files []UploadFile, limit int) error {
	if len(files) > 10 {
		return fmt.Errorf("%w: at most 10 files per message", ErrBadRequest)
	}
	names := map[string]bool{}
	for _, f := range files {
		if f.Name == "" || len(f.Name) > 255 || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, "/\\") || strings.IndexFunc(f.Name, unicode.IsControl) >= 0 || names[f.Name] {
			return fmt.Errorf("%w: invalid or duplicate upload filename", ErrBadRequest)
		}
		names[f.Name] = true
		if len(f.Data) > limit {
			return fmt.Errorf("%w: file exceeds upload limit of %d bytes", ErrBadRequest, limit)
		}
		if utf8.RuneCountInString(f.Description) > 1024 {
			return fmt.Errorf("%w: attachment description exceeds 1024 characters", ErrBadRequest)
		}
		if f.ContentType != "" {
			_, _, err := mime.ParseMediaType(f.ContentType)
			if err != nil || len(f.ContentType) > 255 || strings.IndexFunc(f.ContentType, unicode.IsControl) >= 0 {
				return fmt.Errorf("%w: invalid attachment content type", ErrBadRequest)
			}
		}
	}
	return nil
}
