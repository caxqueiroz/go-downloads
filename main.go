package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
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

const (
	sessionCookieName = "session"
	sessionTTL        = time.Hour
)

// basePath is the URL prefix the app is served under (e.g. "/downloads"
// behind a path-mounted reverse proxy that strips it). Empty means root.
var basePath string

// appURL prefixes an absolute app path with basePath.
func appURL(path string) string {
	return basePath + path
}

// sessionSecret signs session cookies. It comes from SESSION_SECRET, or is
// generated at startup (which logs everyone out on restart).
var sessionSecret []byte

func initSessionSecret() {
	if s := os.Getenv("SESSION_SECRET"); s != "" {
		sessionSecret = []byte(s)
		return
	}
	sessionSecret = make([]byte, 32)
	if _, err := rand.Read(sessionSecret); err != nil {
		slog.Error("Unable to generate session secret", "error", err)
		os.Exit(1)
	}
	slog.Warn("SESSION_SECRET not set; sessions reset on restart")
}

func signSession(expiry string) string {
	mac := hmac.New(sha256.New, sessionSecret)
	mac.Write([]byte(expiry))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// newSessionToken returns "<unix expiry>.<hmac>".
func newSessionToken() string {
	expiry := strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	return expiry + "." + signSession(expiry)
}

func validSessionToken(token string) bool {
	expiry, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(signSession(expiry))) {
		return false
	}
	exp, err := strconv.ParseInt(expiry, 10, 64)
	return err == nil && time.Now().Unix() < exp
}

func setSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	secure := os.Getenv("SESSION_COOKIE_SECURE") != "false"
	c.SetCookie(sessionCookieName, newSessionToken(), int(sessionTTL.Seconds()), appURL("/"), "", secure, true)
}

// authRequired is a middleware to check if the user is authenticated
func authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the password from environment variable
		password := os.Getenv("APP_PASSWORD")
		if password == "" {
			// If no password is set, skip authentication
			c.Next()
			return
		}

		// Check if the user is already authenticated
		session, _ := c.Cookie(sessionCookieName)
		if validSessionToken(session) {
			c.Next()
			return
		}

		// If not authenticated and not accessing login page, redirect to login
		if c.Request.URL.Path != "/login" {
			c.Redirect(http.StatusFound, appURL("/login"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	port := os.Getenv("PORT")
	bucketName := os.Getenv("S3_BUCKET_NAME")
	region := os.Getenv("AWS_REGION")
	password := os.Getenv("APP_PASSWORD")

	if port == "" {
		slog.Error("PORT must be set")
		os.Exit(1)
	}

	if bucketName == "" {
		slog.Error("S3_BUCKET_NAME must be set")
		os.Exit(1)
	}

	if region == "" {
		region = "us-east-1" // Default region
	}

	initSessionSecret()
	basePath = strings.TrimRight(os.Getenv("BASE_PATH"), "/")

	if password == "" {
		slog.Warn("No password protection set; configure APP_PASSWORD to restrict access")
	}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		slog.Error("Unable to load AWS SDK config", "error", err)
		os.Exit(1)
	}

	// Create S3 client. S3_ENDPOINT points at an S3-compatible store
	// (e.g. SeaweedFS), which requires path-style addressing.
	var s3Options []func(*s3.Options)
	if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
		slog.Info("Using custom S3 endpoint")
		s3Options = append(s3Options, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}
	s3Client := s3.NewFromConfig(cfg, s3Options...)

	// Set up Gin router
	router := gin.New()
	router.Use(gin.Logger())
	router.SetFuncMap(template.FuncMap{"url": appURL})
	router.LoadHTMLGlob("templates/*.html")

	// Login routes
	router.GET("/login", func(c *gin.Context) {
		c.HTML(http.StatusOK, "login.html", gin.H{})
	})

	router.POST("/login", func(c *gin.Context) {
		password := os.Getenv("APP_PASSWORD")

		// If no password is set, authenticate automatically
		if password == "" {
			setSessionCookie(c)
			c.Redirect(http.StatusFound, appURL("/"))
			return
		}

		// Check the submitted password
		submittedPassword := c.PostForm("password")
		if subtle.ConstantTimeCompare([]byte(submittedPassword), []byte(password)) == 1 {
			// Set cookie and redirect to home page
			setSessionCookie(c)
			c.Redirect(http.StatusFound, appURL("/"))
		} else {
			// Show error message
			c.HTML(http.StatusUnauthorized, "login.html", gin.H{
				"Error": "Invalid password. Please try again.",
			})
		}
	})

	// Protected routes
	protected := router.Group("/")
	protected.Use(authRequired())

	// Home page - list files from S3
	protected.GET("/", func(c *gin.Context) {
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
	protected.GET("/download/:filename", func(c *gin.Context) {
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
		defer func() {
			if err := output.Body.Close(); err != nil {
				slog.Error("Close S3 response body", "error", err)
			}
		}()

		// Set Content-Disposition header for download
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.Header("Content-Type", *output.ContentType)

		if output.ContentLength != nil {
			c.Header("Content-Length", strconv.FormatInt(*output.ContentLength, 10))
		}

		// Stream the file to the response
		_, err = io.Copy(c.Writer, output.Body)
		if err != nil {
			slog.Error("Error streaming file", "error", err)
		}
	})

	// Add API endpoint for JSON response
	protected.GET("/api/files", func(c *gin.Context) {
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
				"download_url":  appURL("/download/") + *obj.Key,
			})
		}

		c.JSON(http.StatusOK, files)
	})

	if err := router.Run(":" + port); err != nil {
		slog.Error("Run HTTP server", "error", err)
		os.Exit(1)
	}
}
