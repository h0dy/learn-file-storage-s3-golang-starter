package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
)

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

// with presigned url 
// generatePresignedURL func generates a presigned URL video with an expired time
// func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
// 	presignClient := s3.NewPresignClient(s3Client)
// 	presignHTTP, err := presignClient.PresignGetObject(
// 		context.TODO(),
// 		&s3.GetObjectInput{
// 			Bucket: aws.String(bucket),
// 			Key: aws.String(key),
// 		},
// 		s3.WithPresignExpires(expireTime))
// 	if err != nil {
// 		return "", fmt.Errorf("error in generating url in generatePresignedURL func: %v", err)
// 	}
// 	return presignHTTP.URL, nil
// }

// dbVideoToSignedVideo func takes the video and replace the "videoURL" with the actual video "presigned URL"
// func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
// 	if video.VideoURL == nil {
// 		return video, nil
// 	}
// 	videoURL := strings.Split(*video.VideoURL, ",")
// 	if len(videoURL) < 2 {
// 		return video, nil
// 	}
// 	fileKey := videoURL[1]
// 	fileBucket := videoURL[0]
// 	presignedURLVid, err := generatePresignedURL(cfg.s3Client, fileBucket, fileKey, 10*time.Minute)
// 	if err != nil {
// 		return video, err
// 	}
// 	video.VideoURL = &presignedURLVid
// 	return video, nil
// }