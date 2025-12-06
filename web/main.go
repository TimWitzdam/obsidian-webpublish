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

	"github.com/gin-gonic/gin"

	"github.com/TimWitzdam/obsidian-webpublish/config"
	"github.com/TimWitzdam/obsidian-webpublish/internal/documents"
)

const defaultConfigPath = "config.yaml"

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

	router := gin.Default()

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/doc/:id", serveDocumentHandler(store))

	protected := router.Group("", apiKeyMiddleware(cfg.APIKeys))
	protected.POST("/documents", uploadDocumentHandler(store, cfg.BaseURL))

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

func uploadDocumentHandler(store *documents.Store, baseURL string) gin.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	return func(c *gin.Context) {
		content, err := readMarkdownPayload(c)
		if err != nil {
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
			return nil, fmt.Errorf("read upload: %w", err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, errors.New("document content cannot be empty")
		}
		return data, nil
	}

	if content := c.PostForm("content"); strings.TrimSpace(content) != "" {
		return []byte(content), nil
	}

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("document content cannot be empty")
	}
	return data, nil
}
