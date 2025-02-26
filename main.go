package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/heroku/x/hmetrics/onload"
)

func main() {
	port := os.Getenv("PORT")

	if port == "" {
		log.Fatal("$PORT must be set")
	}

	router := gin.New()
	router.Use(gin.Logger())
	router.Static("/downloads", "downloads")

	// Handle file downloads
	router.GET("/files", func(c *gin.Context) {
		files := []gin.H{}
		err := filepath.Walk("downloads", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				files = append(files, gin.H{
					"name":         info.Name(),
					"size":         info.Size(),
					"modified":     info.ModTime().Format(time.RFC3339),
					"download_url": "/download/" + info.Name(),
					"direct_url":   "/downloads/" + info.Name(),
				})
			}
			return nil
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, files)
	})

	router.GET("/download/:filename", func(c *gin.Context) {
		filename := c.Param("filename")
		filePath := filepath.Join("downloads", filename)

		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			c.String(http.StatusNotFound, "File not found")
			return
		}

		// Set Content-Disposition header for download
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.File(filePath)
	})

	router.Run(":" + port)
}
