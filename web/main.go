package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TimWitzdam/obsidian-webpublish/config"
	"github.com/TimWitzdam/obsidian-webpublish/internal/documents"
	"github.com/TimWitzdam/obsidian-webpublish/internal/ratelimit"
)

const defaultConfigPath = "config.yaml"

var errPayloadTooLarge = errors.New("document exceeds maximum allowed size")

var attachmentTokenPattern = regexp.MustCompile(`^[A-Za-z0-9]{6,64}$`)

var allowedAttachmentTypes = map[string]string{
	"image/jpeg":    ".jpg",
	"image/png":     ".png",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/bmp":     ".bmp",
	"image/svg+xml": ".svg",
	"image/tiff":    ".tiff",
}

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := documents.NewStore(cfg.StorageDir)
	if err != nil {
		log.Fatalf("init document store: %v", err)
	}

	limiter := ratelimit.NewStore(
		cfg.RateLimit.RequestsPerMinute,
		cfg.RateLimit.Burst,
		time.Hour,
	)

	router := gin.Default()
	router.Use(ratelimit.Middleware(limiter))

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/doc/:id", serveDocumentHandler(store))
	router.GET("/assets/:id/:name", serveAssetHandler(store))
	protected := router.Group(
		"",
		apiKeyMiddleware(cfg.APIKeys),
	)
	protected.POST("/documents", uploadDocumentHandler(store, cfg.BaseURL, cfg.Upload.MaxBytes))
	log.Printf("obsidian-webpublish listening on %s", cfg.ListenAddr)
	if err := router.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func apiKeyMiddleware(keys []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}

	return func(c *gin.Context) {
		provided := c.GetHeader("X-API-Key")
		if provided == "" {
			provided = c.Query("apiKey")
		}
		if _, ok := allowed[provided]; !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid API key"})
			return
		}
		c.Next()
	}
}

func uploadDocumentHandler(store *documents.Store, baseURL string, maxUploadBytes int64) gin.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	return func(c *gin.Context) {
		if maxUploadBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
		}
		content, err := readMarkdownPayload(c)
		if err != nil {
			if errors.Is(err, errPayloadTooLarge) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "document exceeds maximum upload size"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		attachments, err := extractAttachments(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		title := strings.TrimSpace(c.GetHeader("X-Document-Title"))
		if title == "" {
			title = strings.TrimSpace(c.PostForm("title"))
		}

		doc, err := store.Save(content, title, attachments)
		if err != nil {
			if errors.Is(err, documents.ErrEmptyDocument) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "document content cannot be empty"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"id":  doc.ID,
			"url": fmt.Sprintf("%s/doc/%s", base, doc.ID),
		})
	}
}

func serveDocumentHandler(store *documents.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		htmlBytes, err := store.LoadHTML(id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.Data(http.StatusOK, "text/html; charset=utf-8", htmlBytes)
	}
}

func serveAssetHandler(store *documents.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		name := c.Param("name")
		data, contentType, err := store.LoadAsset(id, name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				c.JSON(http.StatusNotFound, gin.H{"error": "asset not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Data(http.StatusOK, contentType, data)
	}
}

func readMarkdownPayload(c *gin.Context) ([]byte, error) {
	if file, err := c.FormFile("file"); err == nil {
		opened, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("read upload: %w", err)
		}
		defer opened.Close()
		data, err := io.ReadAll(opened)
		if err != nil {
			if isRequestTooLarge(err) {
				return nil, errPayloadTooLarge
			}
			return nil, fmt.Errorf("read upload: %w", err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, errors.New("document content cannot be empty")
		}
		return data, nil
	} else if isRequestTooLarge(err) {
		return nil, errPayloadTooLarge
	}

	if content := c.PostForm("content"); strings.TrimSpace(content) != "" {
		return []byte(content), nil
	}

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if isRequestTooLarge(err) {
			return nil, errPayloadTooLarge
		}
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("document content cannot be empty")
	}
	return data, nil
}

func extractAttachments(c *gin.Context) ([]documents.Attachment, error) {
	if !strings.HasPrefix(c.ContentType(), "multipart/") {
		return nil, nil
	}
	form, err := c.MultipartForm()
	if err != nil {
		return nil, fmt.Errorf("parse multipart form: %w", err)
	}
	if form == nil || len(form.File) == 0 {
		return nil, nil
	}
	attachments := make([]documents.Attachment, 0)
	for field, files := range form.File {
		if !strings.HasPrefix(field, "attachment-") {
			continue
		}
		token := strings.TrimPrefix(field, "attachment-")
		if strings.TrimSpace(token) == "" || !attachmentTokenPattern.MatchString(token) {
			continue
		}
		for _, fh := range files {
			if fh == nil {
				continue
			}
			data, err := readAttachmentData(fh)
			if err != nil {
				return nil, fmt.Errorf("read attachment %s: %w", fh.Filename, err)
			}
			if len(data) == 0 {
				continue
			}
			storedName, err := normalizeAttachmentFilename(token, data)
			if err != nil {
				return nil, fmt.Errorf("attachment %s: %w", fh.Filename, err)
			}
			attachments = append(attachments, documents.Attachment{
				Token:      token,
				StoredName: storedName,
				Data:       data,
			})
		}
	}
	return attachments, nil
}

func normalizeAttachmentFilename(token string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("attachment is empty")
	}
	if !attachmentTokenPattern.MatchString(token) {
		return "", errors.New("invalid attachment token")
	}
	contentType := http.DetectContentType(data)
	ext, ok := allowedAttachmentTypes[contentType]
	if !ok {
		return "", fmt.Errorf("unsupported attachment type %s", contentType)
	}
	return fmt.Sprintf("%s%s", token, ext), nil
}

func readAttachmentData(fh *multipart.FileHeader) ([]byte, error) {
	file, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		if isRequestTooLarge(err) {
			return nil, errPayloadTooLarge
		}
		return nil, err
	}
	return data, nil
}

func isRequestTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	return false
}
