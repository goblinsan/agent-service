package sse_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	e := sse.Event{Type: "run.created", Data: map[string]string{"id": "123"}}

	err := sse.Write(rr, e)
	require.NoError(t, err)

	body := rr.Body.String()
	assert.True(t, strings.HasPrefix(body, "data: "), "should start with 'data: '")
	assert.True(t, strings.HasSuffix(body, "\n\n"), "should end with double newline")
	assert.Contains(t, body, `"type":"run.created"`)
	assert.Contains(t, body, `"id":"123"`)
}

func TestWrite_MultipleEvents(t *testing.T) {
	rr := httptest.NewRecorder()

	events := []sse.Event{
		{Type: "run.created", Data: "start"},
		{Type: "run.completed", Data: "end"},
	}

	for _, e := range events {
		require.NoError(t, sse.Write(rr, e))
	}

	body := rr.Body.String()
	assert.Equal(t, 2, strings.Count(body, "data: "), "should have two data lines")
	assert.Contains(t, body, "run.created")
	assert.Contains(t, body, "run.completed")
}
