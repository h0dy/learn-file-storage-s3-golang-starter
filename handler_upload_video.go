package main

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

// handlerUploadVideo func parse a video and upload it to s3 bucket
func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// authenticate the user
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	
	// get the metadata for the video
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}
	// check if the user is the owner of the video
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	fmt.Println("uploading video with the id:", videoID, "by user", userID)
	
	// upload limit
	const maxMemory = 1 << 30 // 1GB
	http.MaxBytesReader(w, r.Body, maxMemory)
	
	// read and copy the file
	vidFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form file", err)
		return
	}
	defer vidFile.Close()

	// get the media type of the video
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Missing Content-Type for thumbnail", nil)
		return
	}
	if mediaType != "video/mp4"  {
		respondWithError(w, http.StatusBadRequest, "invalid Content-Type", nil)
		return
	}

	// create temporary file for the video
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}

	// close and remove the temporary file after exiting the function
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// copy the file (video) to the temporary file
	io.Copy(tempFile, vidFile)

	// reset tempFile pointer to the beginning to read the file
	tempFile.Seek(0, io.SeekStart)

	fileBaseUrl := getAssetPath(mediaType)

	// get the vid's ratio aspect for naming convention
	vidRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		log.Fatal(err)
		return
	}
	// fileKey is the file URI
	fileKey := filepath.Join(vidRatio, fileBaseUrl)

	// get processed version of the video
	processedVidPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}
	defer os.Remove(processedVidPath)

	processedVid, err := os.Open(processedVidPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}
	defer processedVid.Close()

	// upload the object (file) to s3 bucket
	if _, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket: &cfg.s3Bucket,
		Key: &fileKey,
		Body: processedVid,
		ContentType: &mediaType,
	}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}

	// set/update the video url. 
	// URL structure: "<bucket-name>,<key>"
	vidUrl := fmt.Sprintf("%v,%v", cfg.s3Bucket, fileKey)
	video.VideoURL =  &vidUrl
	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}

	// gets the video struct with the videoURL filed sets to the actual video 
	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, database.Video{
		ID: videoID,
		CreatedAt: video.CreatedAt,
		UpdatedAt: video.UpdatedAt,
		ThumbnailURL: video.ThumbnailURL,
		VideoURL: video.VideoURL,
		CreateVideoParams: video.CreateVideoParams,
	})
}
