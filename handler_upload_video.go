package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

// handlerUploadVideo handles HTTP requests for uploading video files.
// It validates the user's JWT token, checks video ownership, saves the
// uploaded file to a temporary location, determines the aspect ratio, and
// uploads the video to S3.
//
// The video URL is then stored separately with the metadata in sqlite database.
//
// The handler expects:
// - A video ID in the URL path
// - A JWT token in the Authorization header
// - A multipart form with a "video" field containing an MP4 file
//
// Returns HTTP 400 for invalid requests, 401 for unauthorized access,
// and 500 for internal server errors.
func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Missing token", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid token", err)
		return
	}

	fmt.Println("Uploading video file for video", videoID, "by user", userID)

	const maxMemory = 10 << 30 // 1 GB
	if err = r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse uploaded file", err)
		return
	}
	defer file.Close()

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video metadata", err)
		return
	}

	if metadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User does not have access to this video", nil)
		return
	}

	// Create filepath
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse Content-Type header", nil)
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Video must be in mp4 format", nil)
	}

	// Save video to temporary file
	tmpFile, err := os.CreateTemp("/tmp", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save temporary file", nil)
	}
	defer os.Remove("/tmp/tubely-upload.mp4")
	defer tmpFile.Close() // defer is LIFO so it closes first

	// Copy the mulitpart file to tmpFile
	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to write to temporary file", nil)
	}

	// Reset tmpFile's pointer to the beginning
	_, err = tmpFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to reset pointer", nil)
	}

	// Create random file key for storing in s3
	key, err := cfg.getFilename(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}

	// Get aspect ratio of video (16:9, 9:16, or other)
	aspectRatio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}

	// Get prefix for storing in s3 based on aspect ratio and add it to the key
	prefixes := map[string]string{
		"16:9":  "landscape",
		"9:16":  "portrait",
		"other": "other",
	}
	prefix := prefixes[aspectRatio]
	key = prefix + "/" + key

	// Add the video to the DB. The VideoURL field is of the form "bucket,key"
	videoUrl := cfg.s3Bucket + "," + key
	metadata.VideoURL = &videoUrl
	cfg.db.UpdateVideo(metadata)

	// Update video with presigned URL
	metadata, err = cfg.dbVideoToSignedVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}

	// Process video for a fast start with ffmpeg
	processedFilePath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}
	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}
	defer os.Remove(processedFilePath)
	defer processedFile.Close()

	// Upload video to S3
	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        processedFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to S3", err)
	}
}
