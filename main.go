package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
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

// bucket is one S3 bucket served under its own URL prefix and password.
type bucket struct {
	name     string // URL name; empty for the default bucket served at the root
	s3Name   string
	password string // empty means no password protection
}

var (
	bucketNamePattern   = regexp.MustCompile(`^[a-z0-9-]+$`)
	reservedBucketNames = map[string]bool{"login": true, "download": true, "api": true}
)

// parseBuckets reads the BUCKETS env value: a JSON object keyed by URL name,
// e.g. {"photos":{"bucket":"family-photos","password":"secret"}}.
func parseBuckets(raw string) (map[string]*bucket, error) {
	buckets := map[string]*bucket{}
	if raw == "" {
		return buckets, nil
	}
	var entries map[string]struct {
		Bucket   string `json:"bucket"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse BUCKETS: %w", err)
	}
	for name, entry := range entries {
		if !bucketNamePattern.MatchString(name) {
			return nil, fmt.Errorf("bucket name %q must match %s", name, bucketNamePattern)
		}
		if reservedBucketNames[name] {
			return nil, fmt.Errorf("bucket name %q is reserved", name)
		}
		if entry.Bucket == "" {
			return nil, fmt.Errorf("bucket %q has no S3 bucket set", name)
		}
		buckets[name] = &bucket{name: name, s3Name: entry.Bucket, password: entry.Password}
	}
	return buckets, nil
}

// url prefixes a path within the bucket with basePath and the bucket's URL name.
func (b *bucket) url(path string) string {
	if b.name == "" {
		return appURL(path)
	}
	return appURL("/" + b.name + path)
}

func (b *bucket) cookieName() string {
	if b.name == "" {
		return sessionCookieName
	}
	return sessionCookieName + "_" + b.name
}

// signSession binds the signature to the bucket and its password, so a
// session for one bucket is useless on another and a password change
// revokes existing sessions.
func (b *bucket) signSession(expiry string) string {
	mac := hmac.New(sha256.New, sessionSecret)
	mac.Write(fmt.Appendf(nil, "%s\x00%x\x00%s", b.name, sha256.Sum256([]byte(b.password)), expiry))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// newSessionToken returns "<unix expiry>.<hmac>".
func (b *bucket) newSessionToken() string {
	expiry := strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	return expiry + "." + b.signSession(expiry)
}

func (b *bucket) validSessionToken(token string) bool {
	expiry, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(b.signSession(expiry))) {
		return false
	}
	exp, err := strconv.ParseInt(expiry, 10, 64)
	return err == nil && time.Now().Unix() < exp
}

func (b *bucket) setSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	secure := os.Getenv("SESSION_COOKIE_SECURE") != "false"
	c.SetCookie(b.cookieName(), b.newSessionToken(), int(sessionTTL.Seconds()), b.url("/"), "", secure, true)
}

// authenticated reports whether the request may access the bucket.
func (b *bucket) authenticated(c *gin.Context) bool {
	if b.password == "" {
		// If no password is set, skip authentication
		return true
	}
	session, _ := c.Cookie(b.cookieName())
	return b.validSessionToken(session)
}

// pageData returns the template data common to the bucket's pages.
func (b *bucket) pageData() gin.H {
	return gin.H{"Base": b.url(""), "Name": b.name}
}

// newRouter serves def at the root and each named bucket under /<name>/.
func newRouter(s3Client *s3.Client, def *bucket, named map[string]*bucket) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.LoadHTMLGlob("templates/*.html")

	registerBucketRoutes(router.Group("/"), s3Client, func(*gin.Context) *bucket { return def })

	byName := func(c *gin.Context) *bucket { return named[c.Param("name")] }
	router.GET("/:name", func(c *gin.Context) {
		b := byName(c)
		if b == nil {
			c.String(http.StatusNotFound, "404 page not found")
			return
		}
		c.Redirect(http.StatusFound, b.url("/"))
	})
	registerBucketRoutes(router.Group("/:name"), s3Client, byName)

	return router
}

// registerBucketRoutes adds the login, listing, download and API routes for
// the bucket that resolve picks for each request.
func registerBucketRoutes(g *gin.RouterGroup, s3Client *s3.Client, resolve func(*gin.Context) *bucket) {
	// public answers unknown buckets with the same 404 as any unknown path.
	public := func(h func(*gin.Context, *bucket)) gin.HandlerFunc {
		return func(c *gin.Context) {
			b := resolve(c)
			if b == nil {
				c.String(http.StatusNotFound, "404 page not found")
				return
			}
			h(c, b)
		}
	}
	// protected redirects unauthenticated requests to the bucket's login.
	protected := func(h func(*gin.Context, *bucket)) gin.HandlerFunc {
		return public(func(c *gin.Context, b *bucket) {
			if !b.authenticated(c) {
				c.Redirect(http.StatusFound, b.url("/login"))
				return
			}
			h(c, b)
		})
	}

	// Login routes
	g.GET("/login", public(func(c *gin.Context, b *bucket) {
		c.HTML(http.StatusOK, "login.html", b.pageData())
	}))

	g.POST("/login", public(func(c *gin.Context, b *bucket) {
		// If no password is set, authenticate automatically
		submittedPassword := c.PostForm("password")
		if b.password == "" || subtle.ConstantTimeCompare([]byte(submittedPassword), []byte(b.password)) == 1 {
			// Set cookie and redirect to home page
			b.setSessionCookie(c)
			c.Redirect(http.StatusFound, b.url("/"))
			return
		}

		// Show error message
		data := b.pageData()
		data["Error"] = "Invalid password. Please try again."
		c.HTML(http.StatusUnauthorized, "login.html", data)
	}))

	// Home page - list files from S3
	g.GET("/", protected(func(c *gin.Context, b *bucket) {
		data := b.pageData()

		// List objects in the bucket
		output, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket: aws.String(b.s3Name),
		})

		if err != nil {
			data["Error"] = fmt.Sprintf("Error listing S3 objects: %v", err)
			c.HTML(http.StatusInternalServerError, "index.html", data)
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

		data["Files"] = files
		c.HTML(http.StatusOK, "index.html", data)
	}))

	// Handle file download from S3
	g.GET("/download/:filename", protected(func(c *gin.Context, b *bucket) {
		filename := c.Param("filename")

		// Get the object from S3
		output, err := s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
			Bucket: aws.String(b.s3Name),
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
	}))

	// Add API endpoint for JSON response
	g.GET("/api/files", protected(func(c *gin.Context, b *bucket) {
		// List objects in the bucket
		output, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket: aws.String(b.s3Name),
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
				"download_url":  b.url("/download/") + *obj.Key,
			})
		}

		c.JSON(http.StatusOK, files)
	}))
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

	buckets, err := parseBuckets(os.Getenv("BUCKETS"))
	if err != nil {
		slog.Error("Invalid BUCKETS configuration", "error", err)
		os.Exit(1)
	}
	for name, b := range buckets {
		if b.password == "" {
			slog.Warn("Bucket has no password protection", "bucket", name)
		}
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

	router := newRouter(s3Client, &bucket{s3Name: bucketName, password: password}, buckets)

	if err := router.Run(":" + port); err != nil {
		slog.Error("Run HTTP server", "error", err)
		os.Exit(1)
	}
}
