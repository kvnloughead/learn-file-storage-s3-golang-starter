package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
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

// FFProbeResponse represents the JSON structure returned by ffprobe
type FFProbeResponse struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func getVideoAspectRatio(filepath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	var response FFProbeResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return "", err
	}

	// Check if we have any video streams
	if len(response.Streams) == 0 {
		return "", fmt.Errorf("no streams found in video file")
	}

	// Get dimensions from the first stream
	width := float64(response.Streams[0].Width)
	height := float64(response.Streams[0].Height)

	// Calculate aspect ratio
	ratio := width / height

	// Determine aspect ratio category
	// Using a small threshold for floating point comparison
	if math.Abs(ratio-16.0/9.0) < 0.1 {
		return "16:9", nil
	} else if math.Abs(ratio-9.0/16.0) < 0.1 {
		return "9:16", nil
	}
	return "other", nil
}
