package anyhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99,
	0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("ANYHOST_MODEL_API_KEY", "")
	t.Setenv("ANYHOST_MODEL_API_BASE_URL", "")
	t.Setenv("ANYHOST_MODEL_API_RESOURCE_ID", "")

	_, err := LoadConfig()
	require.ErrorContains(t, err, "ANYHOST_MODEL_API_KEY")

	t.Setenv("ANYHOST_MODEL_API_KEY", "runtime-key")
	t.Setenv("ANYHOST_MODEL_API_BASE_URL", "https://models.example.com/v1")
	t.Setenv("ANYHOST_MODEL_API_RESOURCE_ID", "mrs_test")

	config, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "runtime-key", config.APIKey)
	require.Equal(t, "https://models.example.com/v1", config.BaseURL)
	require.Equal(t, "mrs_test", config.ResourceID)
}

func TestGenerateImage(t *testing.T) {
	t.Run("constructs request and parses settled image response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/chat/completions", r.URL.Path)
			require.Equal(t, "Bearer runtime-key", r.Header.Get("Authorization"))
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body chatCompletionsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, ModelID, body.Model)
			require.Equal(t, []string{"image", "text"}, body.Modalities)
			require.Equal(t, "16:9", body.ImageConfig.AspectRatio)
			require.Equal(t, "memos-user-42", body.User)
			require.Equal(t, []chatMessage{{Role: "user", Content: "A quiet moonlit lake"}}, body.Messages)

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-anyhost-generation-id", "gen-settled")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":    "ignored-body-id",
				"model": ModelID,
				"choices": []map[string]any{{
					"message": map[string]any{
						"images": []map[string]any{{
							"image_url": map[string]string{
								"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(onePixelPNG),
							},
						}},
					},
				}},
				"usage": map[string]string{
					"settlement_status": "settled",
					"cost":              "0.04",
					"credits":           "4",
				},
			}))
		}))
		defer server.Close()

		client, err := NewClient(Config{
			APIKey:     "runtime-key",
			BaseURL:    server.URL + "/v1",
			ResourceID: "mrs_test",
		}, Options{HTTPClient: server.Client()})
		require.NoError(t, err)

		response, err := client.Generate(context.Background(), Request{
			Prompt:      "A quiet moonlit lake",
			AspectRatio: "16:9",
			UserID:      "memos-user-42",
		})
		require.NoError(t, err)
		require.Equal(t, onePixelPNG, response.Content)
		require.Equal(t, "image/png", response.ContentType)
		require.Equal(t, "nano-banana-pro.png", response.Filename)
		require.Equal(t, ModelID, response.Model)
		require.Equal(t, "gen-settled", response.GenerationID)
		require.Equal(t, Usage{SettlementStatus: "settled", Cost: "0.04", Credits: "4"}, response.Usage)
	})

	t.Run("does not expose unsettled cost values", func(t *testing.T) {
		server := newImageResponseServer(t, "pending")
		defer server.Close()

		client, err := NewClient(Config{
			APIKey:     "runtime-key",
			BaseURL:    server.URL,
			ResourceID: "mrs_test",
		}, Options{HTTPClient: server.Client()})
		require.NoError(t, err)

		response, err := client.Generate(context.Background(), Request{Prompt: "A paper kite"})
		require.NoError(t, err)
		require.Equal(t, "pending", response.Usage.SettlementStatus)
		require.Empty(t, response.Usage.Cost)
		require.Empty(t, response.Usage.Credits)
	})

	t.Run("returns stable API error without retrying", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusServiceUnavailable)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "model_upstream_unavailable"},
			}))
		}))
		defer server.Close()

		client, err := NewClient(Config{
			APIKey:     "runtime-key",
			BaseURL:    server.URL,
			ResourceID: "mrs_test",
		}, Options{HTTPClient: server.Client()})
		require.NoError(t, err)

		_, err = client.Generate(context.Background(), Request{Prompt: "A paper kite"})
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, "model_upstream_unavailable", apiErr.Code)
		require.Equal(t, "12", apiErr.RetryAfter)
		require.Equal(t, int32(1), calls.Load())
	})

	t.Run("rejects non-inline image output", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("x-anyhost-generation-id", "gen-url")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"images": []map[string]any{{
							"image_url": map[string]string{"url": "https://images.example.com/result.png"},
						}},
					},
				}},
			}))
		}))
		defer server.Close()

		client, err := NewClient(Config{
			APIKey:     "runtime-key",
			BaseURL:    server.URL,
			ResourceID: "mrs_test",
		}, Options{HTTPClient: server.Client()})
		require.NoError(t, err)

		_, err = client.Generate(context.Background(), Request{Prompt: "A paper kite"})
		require.ErrorContains(t, err, "unsupported non-inline")
	})
}

func newImageResponseServer(t *testing.T, settlementStatus string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-anyhost-generation-id", "gen-"+settlementStatus)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"model": ModelID,
			"choices": []map[string]any{{
				"message": map[string]any{
					"images": []map[string]any{{
						"image_url": map[string]string{
							"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(onePixelPNG),
						},
					}},
				},
			}},
			"usage": map[string]string{
				"settlement_status": settlementStatus,
				"cost":              "should-not-leak",
				"credits":           "should-not-leak",
			},
		}))
	}))
}
