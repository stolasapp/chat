package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotFound_ReturnsStyledPage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/nonexistent", nil)

	notFoundHandler(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "404", "should contain error code")
	require.Contains(t, body, "Page not found", "should contain error message")
	require.Contains(t, body, "Go Home", "should contain home link")
}
