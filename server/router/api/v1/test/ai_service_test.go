package test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
)

func TestTranscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("requires authentication", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		_, err := ts.Service.Transcribe(ctx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("RIFF")},
				Filename:    "voice.wav",
				ContentType: "audio/wav",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "user not authenticated")
	})

	t.Run("transcribes audio file using persisted transcription setting", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "alice")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		openAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/audio/transcriptions", r.URL.Path)
			require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
			require.NoError(t, r.ParseMultipartForm(10<<20))
			require.Equal(t, "whisper-1", r.FormValue("model"))
			require.Equal(t, "fr", r.FormValue("language"))
			require.Equal(t, "names: Alice", r.FormValue("prompt"))

			file, header, err := r.FormFile("file")
			require.NoError(t, err)
			defer file.Close()
			require.Equal(t, "voice.wav", header.Filename)

			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
				"text": "transcribed text",
			}))
		}))
		defer openAIServer.Close()

		_, err = ts.Store.UpsertInstanceSetting(ctx, &storepb.InstanceSetting{
			Key: storepb.InstanceSettingKey_AI,
			Value: &storepb.InstanceSetting_AiSetting{
				AiSetting: &storepb.InstanceAISetting{
					Providers: []*storepb.AIProviderConfig{
						{
							Id:       "openai-main",
							Title:    "OpenAI",
							Type:     storepb.AIProviderType_OPENAI,
							Endpoint: openAIServer.URL,
							ApiKey:   "sk-test",
						},
					},
					Transcription: &storepb.TranscriptionConfig{
						ProviderId: "openai-main",
						Model:      "whisper-1",
						Language:   "fr",
						Prompt:     "names: Alice",
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err := ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("RIFF")},
				Filename:    "voice.wav",
				ContentType: "audio/wav",
			},
		})
		require.NoError(t, err)
		require.Equal(t, "transcribed text", resp.Text)
	})

	t.Run("returns provider error without rewriting it", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "notfound-user")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		openAIServer := httptest.NewServer(http.NotFoundHandler())
		defer openAIServer.Close()

		_, err = ts.Store.UpsertInstanceSetting(ctx, &storepb.InstanceSetting{
			Key: storepb.InstanceSettingKey_AI,
			Value: &storepb.InstanceSetting_AiSetting{
				AiSetting: &storepb.InstanceAISetting{
					Providers: []*storepb.AIProviderConfig{
						{
							Id:       "openai-main",
							Title:    "OpenAI",
							Type:     storepb.AIProviderType_OPENAI,
							Endpoint: openAIServer.URL,
							ApiKey:   "sk-test",
						},
					},
					Transcription: &storepb.TranscriptionConfig{
						ProviderId: "openai-main",
					},
				},
			},
		})
		require.NoError(t, err)

		_, err = ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("RIFF")},
				Filename:    "voice.wav",
				ContentType: "audio/wav",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transcribe audio")
	})

	t.Run("transcribes audio file with Gemini provider", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "gemini-user")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", r.URL.Path)
			require.Equal(t, "gemini-key", r.Header.Get("x-goog-api-key"))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{
					{
						"finishReason": "STOP",
						"content": map[string]any{
							"parts": []map[string]string{{"text": "gemini transcript"}},
						},
					},
				},
			}))
		}))
		defer geminiServer.Close()

		_, err = ts.Store.UpsertInstanceSetting(ctx, &storepb.InstanceSetting{
			Key: storepb.InstanceSettingKey_AI,
			Value: &storepb.InstanceSetting_AiSetting{
				AiSetting: &storepb.InstanceAISetting{
					Providers: []*storepb.AIProviderConfig{
						{
							Id:       "gemini-main",
							Title:    "Gemini",
							Type:     storepb.AIProviderType_GEMINI,
							Endpoint: geminiServer.URL + "/v1beta",
							ApiKey:   "gemini-key",
						},
					},
					Transcription: &storepb.TranscriptionConfig{
						ProviderId: "gemini-main",
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err := ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("mp3 bytes")},
				Filename:    "voice.mp3",
				ContentType: "audio/mp3",
			},
		})
		require.NoError(t, err)
		require.Equal(t, "gemini transcript", resp.Text)
	})

	t.Run("falls back to engine default model when transcription model is empty", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "bob")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		openAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, r.ParseMultipartForm(10<<20))
			require.Equal(t, "whisper-1", r.FormValue("model"))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
				"text": "built-in model",
			}))
		}))
		defer openAIServer.Close()

		_, err = ts.Store.UpsertInstanceSetting(ctx, &storepb.InstanceSetting{
			Key: storepb.InstanceSettingKey_AI,
			Value: &storepb.InstanceSetting_AiSetting{
				AiSetting: &storepb.InstanceAISetting{
					Providers: []*storepb.AIProviderConfig{
						{
							Id:       "openai-main",
							Title:    "OpenAI",
							Type:     storepb.AIProviderType_OPENAI,
							Endpoint: openAIServer.URL,
							ApiKey:   "sk-test",
						},
					},
					Transcription: &storepb.TranscriptionConfig{
						ProviderId: "openai-main",
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err := ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("RIFF")},
				Filename:    "voice.wav",
				ContentType: "audio/wav",
			},
		})
		require.NoError(t, err)
		require.Equal(t, "built-in model", resp.Text)
	})

	t.Run("rejects non-audio content before provider call", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "charlie")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		_, err = ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("not audio")},
				Filename:    "notes.txt",
				ContentType: "text/plain",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not supported")
	})

	t.Run("returns FailedPrecondition when transcription is not configured", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "alice-empty")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		_, err = ts.Service.Transcribe(userCtx, &v1pb.TranscribeRequest{
			Audio: &v1pb.TranscriptionAudio{
				Source:      &v1pb.TranscriptionAudio_Content{Content: []byte("RIFF")},
				Filename:    "voice.wav",
				ContentType: "audio/wav",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "transcription is not configured")
	})
}

func TestGenerateImage(t *testing.T) {
	ctx := context.Background()

	t.Run("requires authentication", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		_, err := ts.Service.GenerateImage(ctx, &v1pb.GenerateImageRequest{Prompt: "A paper kite"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "user not authenticated")
	})

	t.Run("requires Anyhost Model API runtime configuration", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "image-unconfigured")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)
		t.Setenv("ANYHOST_MODEL_API_KEY", "")
		t.Setenv("ANYHOST_MODEL_API_BASE_URL", "")
		t.Setenv("ANYHOST_MODEL_API_RESOURCE_ID", "")

		_, err = ts.Service.GenerateImage(userCtx, &v1pb.GenerateImageRequest{Prompt: "A paper kite"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "image generation is not configured")
	})

	t.Run("generates an inline image and preserves Generation metadata", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "image-user")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		pngHeader := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
		modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/chat/completions", r.URL.Path)
			require.Equal(t, "Bearer runtime-key", r.Header.Get("Authorization"))

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "google/gemini-3-pro-image", body["model"])
			require.Equal(t, fmt.Sprintf("memos-user-%d", user.ID), body["user"])
			require.Equal(t, map[string]any{"aspect_ratio": "3:4"}, body["image_config"])

			w.Header().Set("x-anyhost-generation-id", "gen-image-test")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"model": "google/gemini-3-pro-image",
				"choices": []map[string]any{{
					"message": map[string]any{
						"images": []map[string]any{{
							"image_url": map[string]string{
								"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader),
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
		defer modelServer.Close()

		t.Setenv("ANYHOST_MODEL_API_KEY", "runtime-key")
		t.Setenv("ANYHOST_MODEL_API_BASE_URL", modelServer.URL+"/v1")
		t.Setenv("ANYHOST_MODEL_API_RESOURCE_ID", "mrs_test")

		response, err := ts.Service.GenerateImage(userCtx, &v1pb.GenerateImageRequest{
			Prompt:      "A paper kite",
			AspectRatio: "3:4",
		})
		require.NoError(t, err)
		require.Equal(t, pngHeader, response.Content)
		require.Equal(t, "image/png", response.ContentType)
		require.Equal(t, "nano-banana-pro.png", response.Filename)
		require.Equal(t, "google/gemini-3-pro-image", response.Model)
		require.Equal(t, "gen-image-test", response.GenerationId)
		require.Equal(t, "settled", response.SettlementStatus)
		require.Equal(t, "0.04", response.Cost)
		require.Equal(t, "4", response.Credits)
	})

	t.Run("validates aspect ratio before creating a Generation", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "image-ratio-user")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		_, err = ts.Service.GenerateImage(userCtx, &v1pb.GenerateImageRequest{
			Prompt:      "A paper kite",
			AspectRatio: "2:1",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "aspect ratio")
	})

	t.Run("maps credit exhaustion by stable error code", func(t *testing.T) {
		ts := NewTestService(t)
		defer ts.Cleanup()

		user, err := ts.CreateRegularUser(ctx, "image-credit-user")
		require.NoError(t, err)
		userCtx := ts.CreateUserContext(ctx, user.ID)

		modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusPaymentRequired)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "insufficient_credits"},
			}))
		}))
		defer modelServer.Close()

		t.Setenv("ANYHOST_MODEL_API_KEY", "runtime-key")
		t.Setenv("ANYHOST_MODEL_API_BASE_URL", modelServer.URL)
		t.Setenv("ANYHOST_MODEL_API_RESOURCE_ID", "mrs_test")

		_, err = ts.Service.GenerateImage(userCtx, &v1pb.GenerateImageRequest{Prompt: "A paper kite"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "ResourceExhausted")
		require.Contains(t, err.Error(), "insufficient_credits")
	})
}
