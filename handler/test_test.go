package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestProviderTestHandler(t *testing.T) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Initialize a mock database or use an in-memory database
	models.Init(":memory:")

	// Create a test router
	router := gin.New()
	router.GET("/test/:id", ProviderTestHandler)

	// Test with invalid ID format
	t.Run("Invalid ID Format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/test/abc", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 400, response.Code)
		assert.Equal(t, "Invalid ID format", response.Message)
	})

	// Test with non-existent ModelWithProvider ID
	t.Run("Non-existent ModelWithProvider ID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/test/999999", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 404, response.Code)
		assert.Equal(t, "ModelWithProvider not found", response.Message)
	})
}
