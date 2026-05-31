package openrouter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMAdapter is the interface every provider must implement.
type LLMAdapter interface {
	ProviderName() string
	ListModels() ([]string, error)
	ChatCompletion(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error)
	ChatCompletionStream(payload map[string]any, model string, timeout time.Duration, chunkChan chan<- []byte, resultChan chan<- StreamResult)
}

type ProviderError struct {
	Type    string
	Message string
}

func (e *ProviderError) Error() string { return fmt.Sprintf("%s: %s", e.Type, e.Message) }

type StreamResult struct {
	Hint string
	Err  error
}

type OpenRouterAdapter struct {
	KeyPool     *KeyPool
	BaseURL     string
	ModelRouter *ModelRouter
}

func (a *OpenRouterAdapter) ProviderName() string { return "openrouter" }
func (a *OpenRouterAdapter) ListModels() ([]string, error) {
	return a.ModelRouter.GetFreeModels()
}

func (a *OpenRouterAdapter) headers(apiKey string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+apiKey)
	h.Set("Content-Type", "application/json")
	h.Set("HTTP-Referer", "http://localhost")
	h.Set("X-Title", "free-model-router-go")
	return h
}

func (a *OpenRouterAdapter) parseStatus(resp *http.Response, hint string) error {
	switch resp.StatusCode {
	case 200:
		return nil
	case 429:
		return &ProviderError{"RateLimitError", fmt.Sprintf("429 Rate Limit (key:%s)", hint)}
	case 404:
		return &ProviderError{"NotFoundError", "404 Not Found"}
	case 401:
		return &ProviderError{"AuthError", fmt.Sprintf("401 Unauthorized (key:%s)", hint)}
	default:
		b, _ := io.ReadAll(resp.Body)
		return &ProviderError{"APIError", fmt.Sprintf("HTTP %d: %.300s", resp.StatusCode, string(b))}
	}
}

func (a *OpenRouterAdapter) ChatCompletion(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	var result map[string]any

	hint, err := a.KeyPool.TryAllKeys(true, model, func(key, h string, keyNum int) error {
		p := clonePayload(payload)
		p["model"] = model
		p["stream"] = false
		body, _ := json.Marshal(p)

		req, _ := http.NewRequest("POST", a.BaseURL+"/chat/completions", bytes.NewBuffer(body))
		req.Header = a.headers(key)

		resp, err := (&http.Client{Timeout: timeout}).Do(req)
		if err != nil {
			return &ProviderError{"TimeoutError", err.Error()}
		}
		defer resp.Body.Close()

		if err := a.parseStatus(resp, h); err != nil {
			return err
		}
		return json.NewDecoder(resp.Body).Decode(&result)
	})

	return result, hint, err
}

func (a *OpenRouterAdapter) ChatCompletionStream(payload map[string]any, model string, timeout time.Duration, chunkChan chan<- []byte, resultChan chan<- StreamResult) {
	go func() {
		defer close(chunkChan)

		var usedHint string
		_, err := a.KeyPool.TryAllKeys(true, model, func(key, h string, keyNum int) error {
			p := clonePayload(payload)
			p["model"] = model
			p["stream"] = true
			body, _ := json.Marshal(p)

			req, _ := http.NewRequest("POST", a.BaseURL+"/chat/completions", bytes.NewBuffer(body))
			req.Header = a.headers(key)

			resp, reqErr := (&http.Client{Timeout: timeout}).Do(req)
			if reqErr != nil {
				return &ProviderError{"TimeoutError", reqErr.Error()}
			}
			defer resp.Body.Close()

			if err := a.parseStatus(resp, h); err != nil {
				return err
			}

			usedHint = h
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					if line[6:] == "[DONE]" {
						chunkChan <- []byte("data: [DONE]\n\n")
						continue
					}
					chunkChan <- []byte(line + "\n\n")
				} else if line != "" {
					chunkChan <- []byte(line + "\n\n")
				}
			}
			return scanner.Err()
		})

		resultChan <- StreamResult{Hint: usedHint, Err: err}
	}()
}

// ChatCompletionSingleKey is used ONLY by verification probes.
func (a *OpenRouterAdapter) ChatCompletionSingleKey(payload map[string]any, model string, timeout time.Duration) (map[string]any, error) {
	var result map[string]any

	_, err := a.KeyPool.TryAllKeys(false, model, func(key, h string, keyNum int) error {
		p := clonePayload(payload)
		p["model"] = model
		p["stream"] = false
		body, _ := json.Marshal(p)

		req, _ := http.NewRequest("POST", a.BaseURL+"/chat/completions", bytes.NewBuffer(body))
		req.Header = a.headers(key)

		resp, err := (&http.Client{Timeout: timeout}).Do(req)
		if err != nil {
			return &ProviderError{"TimeoutError", err.Error()}
		}
		defer resp.Body.Close()

		if err := a.parseStatus(resp, h); err != nil {
			return err
		}
		return json.NewDecoder(resp.Body).Decode(&result)
	})
	return result, err
}

func clonePayload(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
