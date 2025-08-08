package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
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
	// URL structure: "https://<bucket-name>.s3.<region>.amazonaws.com/<key>"
	vidUrl := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, fileKey)
	video.VideoURL =  &vidUrl
	if err := cfg.db.UpdateVideo(video); err != nil {
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

type FprobeOutput struct {
	Streams []struct {
		Height int `json:"height"`
		Width int `json:"width"`
	} `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error", 
		"-print_format", "json",
		"-show_streams",
		filePath)

	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error in running the command: %v", err)
	}

	var data FprobeOutput
	if err := json.Unmarshal(output.Bytes(), &data); err != nil {
		return "", err
	}
	return aspectRatio(data.Streams[0].Width, data.Streams[0].Height), nil
}

func aspectRatio(width, height int) string {
	ratio := float64(width) / float64(height) // img ratio
	// e.g. 1920 / 1065 = ~1.8

    tolerance := 0.03 // 3% tolerance
	
	//  1.80 - ~1.77777 = abs(0.0223) <  0.03
    if math.Abs(ratio - (16.0/9.0)) < tolerance {
        return "landscape"
    } 
	if  math.Abs(ratio - (9.0/16.0)) < tolerance {
    	return "portrait"
    }
	return "other"
}

// processVideoForFastStart func moves the "The moov Atom" to the start of the file
// making it easier for the browser to stream the vid by getting the metadata faster
func processVideoForFastStart(filepath string) (string, error) {
	outputFile := filepath + ".processing"
	cmd := exec.Command("ffmpeg",
		"-i",
		filepath,
		"-c",
		"copy", "-movflags",
		"faststart", "-f", "mp4",
		outputFile,
	)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	
	fileInfo, err := os.Stat(outputFile)
	if err != nil {
		return "", fmt.Errorf("could not stat processed file: %v", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("processed file is empty")
	}
	return outputFile, nil
}