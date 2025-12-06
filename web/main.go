package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TimWitzdam/obsidian-webpublish/config"
	"github.com/TimWitzdam/obsidian-webpublish/internal/documents"
	"github.com/TimWitzdam/obsidian-webpublish/internal/ratelimit"
)

const defaultConfigPath = "config.yaml"

var errPayloadTooLarge = errors.New("document exceeds maximum allowed size")

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

		title := strings.TrimSpace(c.GetHeader("X-Document-Title"))
		if title == "" {
			title = strings.TrimSpace(c.PostForm("title"))
		}

		doc, err := store.Save(content, title)
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
