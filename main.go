package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	_ "github.com/heroku/x/hmetrics/onload"
)

// formatFileSize formats the file size in a human-readable format
func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// formatTime formats the time in a human-readable format
func formatTime(t time.Time) string {
	return t.Format("Jan 02, 2006 15:04:05")
}

// S3File represents a file in S3
type S3File struct {
	Key          string
	Size         string
	LastModified string
	RawSize      int64
}

func main() {
	port := os.Getenv("PORT")
	bucketName := os.Getenv("S3_BUCKET_NAME")
	region := os.Getenv("AWS_REGION")

	if port == "" {
		log.Fatal("$PORT must be set")
	}

	if bucketName == "" {
		log.Fatal("$S3_BUCKET_NAME must be set")
	}

	if region == "" {
		region = "us-east-1" // Default region
	}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		log.Fatalf("Unable to load AWS SDK config: %v", err)
	}

	// Create S3 client
	s3Client := s3.NewFromConfig(cfg)

	// Set up Gin router
	router := gin.New()
	router.Use(gin.Logger())
	router.LoadHTMLGlob("templates/*.html")

	// Home page - list files from S3
	router.GET("/", func(c *gin.Context) {
		// List objects in the bucket
		output, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket: aws.String(bucketName),
		})

		if err != nil {
			c.HTML(http.StatusInternalServerError, "index.html", gin.H{
				"Error": fmt.Sprintf("Error listing S3 objects: %v", err),
			})
			return
		}

		// Process the objects
		var files []S3File
		for _, obj := range output.Contents {
			files = append(files, S3File{
				Key:          *obj.Key,
				Size:         formatFileSize(*obj.Size),
				LastModified: formatTime(*obj.LastModified),
				RawSize:      *obj.Size,
			})
		}

		c.HTML(http.StatusOK, "index.html", gin.H{
			"Files": files,
		})
	})

	// Handle file download from S3
	router.GET("/download/:filename", func(c *gin.Context) {
		filename := c.Param("filename")

		// Get the object from S3
		output, err := s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(filename),
		})

		if err != nil {
			c.String(http.StatusNotFound, "File not found: %v", err)
			return
		}
		defer output.Body.Close()

		// Set Content-Disposition header for download
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.Header("Content-Type", *output.ContentType)
		
		if output.ContentLength != nil {
			c.Header("Content-Length", strconv.FormatInt(*output.ContentLength, 10))
		}

		// Stream the file to the response
		_, err = io.Copy(c.Writer, output.Body)
		if err != nil {
			log.Printf("Error streaming file: %v", err)
		}
	})

	// Add API endpoint for JSON response
	router.GET("/api/files", func(c *gin.Context) {
		// List objects in the bucket
		output, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket: aws.String(bucketName),
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Process the objects
		files := []gin.H{}
		for _, obj := range output.Contents {
			files = append(files, gin.H{
				"key":           *obj.Key,
				"size":          *obj.Size,
				"last_modified": *obj.LastModified,
				"download_url":  "/download/" + *obj.Key,
			})
		}

		c.JSON(http.StatusOK, files)
	})

	router.Run(":" + port)
}
