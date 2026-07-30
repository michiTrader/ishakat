package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL string, apiKey string, customHTTP *http.Client) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	hc := customHTTP
	if hc == nil {
		hc = http.DefaultClient
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: hc,
	}
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest, cb StreamCallback) error {
	req.Stream = true

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("error serializando request chat: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("error creando http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error enviando request a %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("error HTTP %d de la API: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))
		if string(data) == "[DONE]" {
			return cb(ChunkEvent{Done: true})
		}

		var sr StreamResponse
		if err := json.Unmarshal(data, &sr); err != nil {
			continue
		}

		event := ChunkEvent{}
		if sr.Usage != nil {
			event.Usage = sr.Usage
		}

		if len(sr.Choices) > 0 {
			delta := sr.Choices[0].Delta
			event.Content = delta.Content

			if delta.ReasoningContent != "" {
				event.Reasoning = delta.ReasoningContent
			} else if delta.Reasoning != "" {
				event.Reasoning = delta.Reasoning
			}
		}

		if event.Content != "" || event.Reasoning != "" || event.Usage != nil {
			if err := cb(event); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error leyendo stream SSE: %w", err)
	}

	return nil
}
