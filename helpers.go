package main

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

// getFilename creates a unique filename for a multipart form file. The basename
// consists of a random base64 encoded 32 byte string with an extension from the mimetype.
func (cfg *apiConfig) getFilename(mediaType string) (string, error) {
	ext := "." + strings.Split(mediaType, "/")[1]
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	basename := base64.RawURLEncoding.EncodeToString(b)
	return basename + ext, nil
}
