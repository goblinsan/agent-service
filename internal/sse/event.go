package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func Write(w http.ResponseWriter, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
