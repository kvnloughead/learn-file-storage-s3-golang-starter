package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

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

	key, err := cfg.getFilename(w, mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to s3", err)
		return
	}

	// Upload video to S3
	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        tmpFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to S3", err)
	}

	// Update video metadata in DB
	videoUrl := (&url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.s3.%s.amazonaws.com", cfg.s3Bucket, cfg.s3Region),
		Path:   key,
	}).String()
	metadata.VideoURL = &videoUrl
	cfg.db.UpdateVideo(metadata)
}
