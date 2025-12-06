package ratelimit

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Middleware(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.Next()
			return
		}

		key := c.GetHeader("X-API-Key")
		if key == "" {
			key = c.Query("apiKey")
		}
		if key == "" {
			key = c.ClientIP()
		}

		if !store.Allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}

		c.Next()
	}
}
