package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

// getFilename creates a unique filename for a multipart form file.
// It generates a random 32-byte string, encodes it using base64 URL-safe
// encoding, and appends the appropriate file extension based on the provided
// mediaType.
//
// Parameters:
//   - mediaType: MIME type string (e.g., "video/mp4")
//
// Returns:
//   - A unique filename string with extension
//   - An error if random bytes generation fails
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

// FFProbeResponse represents the JSON structure returned by ffprobe command.
// It contains an array of streams, each with width and height information.
type FFProbeResponse struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

// getVideoAspectRatio analyzes a video file and determines its aspect ratio.
// It uses ffprobe to extract the video dimensions and categorizes the aspect
// ratio into one of three categories: "16:9" (landscape), "9:16" (portrait),
// or "other".
//
// Parameters:
//   - filepath: Path to the video file to analyze
//
// Returns:
//   - A string indicating the aspect ratio ("16:9", "9:16", or "other")
//   - An error if the file cannot be analyzed or no video streams are found
//
// The function uses a threshold of 0.1 when comparing aspect ratios to account
// for slight variations in video dimensions.
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

// processVideoForFastStart accepts an mp4 filepath string as an argument and
// uses ffmpeg to process it for a fast start by moving its moov atom the
// beginning of the file.
//
// It returns the output filepath (which is originalPath.processed) and an
// error.
func processVideoForFastStart(filePath string) (string, error) {
	outputPath := filePath + ".processed"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputPath)

	// Capture stderr for potential error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	// Return the path to the processed file
	return outputPath, nil
}

// generatePresignedURL uses s3PresignClient to generate a presigned URL for
// the provided bucket, key, and expiration. It returns the presigned URL and
// an error if the correpsonding presigned request can't be created.
func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	s3PresignClient := s3.NewPresignClient(s3Client)

	presignedReq, err := s3PresignClient.PresignGetObject(
		context.TODO(),
		&s3.GetObjectInput{
			Bucket: &bucket,
			Key:    &key},
		s3.WithPresignExpires(expireTime),
	)

	if err != nil {
		return "", err
	}

	return presignedReq.URL, nil
}

// dbVideoToSignedVideo prepares a video for sending to a client by generating
// a presigned URL for it. If the video document is in draft form, it will not
// have a VideoURL property. In that case, the function returns the original
// video document.
//
// The video's VideoURL property is replaced with this signed URL and is
// returned.
func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL != nil {

		parts := strings.Split(*video.VideoURL, ",")
		if len(parts) != 2 {
			return database.Video{}, fmt.Errorf("invalid video URL format: expected bucket,key got %s", *video.VideoURL)
		}

		bucket := parts[0]
		key := parts[1]

		signedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Hour)
		if err != nil {
			return database.Video{}, err
		}

		video.VideoURL = &signedURL
		return video, nil
	}

	return video, nil
}
