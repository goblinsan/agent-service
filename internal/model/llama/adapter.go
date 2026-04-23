package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
)

type Adapter struct {
	baseURL string
	client  *http.Client
}

func New(baseURL string) *Adapter {
	return &Adapter{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []model.Message `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message      model.Message `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
}

type streamChunk struct {
	Choices []struct {
		Delta        struct{ Content string `json:"content"` } `json:"delta"`
		FinishReason *string                                   `json:"finish_reason"`
	} `json:"choices"`
}

func (a *Adapter) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	body, err := json.Marshal(chatRequest{
		Model:     "local",
		Messages:  req.Messages,
		MaxTokens: req.MaxTokens,
		Stream:    false,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama: unexpected status %d: %s", resp.StatusCode, b)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llama: no choices in response")
	}
	return &model.Response{
		Content:      cr.Choices[0].Message.Content,
		FinishReason: cr.Choices[0].FinishReason,
	}, nil
}

func (a *Adapter) Stream(ctx context.Context, req model.Request, onChunk func(chunk string) error) error {
	body, err := json.Marshal(chatRequest{
		Model:     "local",
		Messages:  req.Messages,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("llama: unexpected status %d: %s", resp.StatusCode, b)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if content := chunk.Choices[0].Delta.Content; content != "" {
			if err := onChunk(content); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
