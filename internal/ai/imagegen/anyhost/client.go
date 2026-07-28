// Package anyhost implements image generation through the Anyhost Model API.
package anyhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	// ModelID is the exact Anyhost Catalog ID for Google Nano Banana Pro.
	ModelID = "google/gemini-3-pro-image"

	defaultHTTPTimeout          = 3 * time.Minute
	maxResponseBodySizeBytes    = 32 * 1024 * 1024
	maxGeneratedImageSizeBytes  = 20 * 1024 * 1024
	defaultGeneratedAspectRatio = "1:1"
)

// Options customizes the Anyhost image generation client.
type Options struct {
	HTTPClient *http.Client
}

// Request describes one image generation request.
type Request struct {
	Prompt      string
	AspectRatio string
	UserID      string
}

// Usage contains final settlement values when Anyhost has settled the Generation.
type Usage struct {
	SettlementStatus string
	Cost             string
	Credits          string
}

// Response contains one generated inline image and its Anyhost Generation metadata.
type Response struct {
	Content      []byte
	ContentType  string
	Filename     string
	Model        string
	GenerationID string
	Usage        Usage
}

// APIError is a safe, code-based Anyhost Model API failure.
type APIError struct {
	StatusCode int
	Code       string
	RetryAfter string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return "Anyhost Model API request failed: " + e.Code
	}
	return "Anyhost Model API request failed"
}

// Client calls the Anyhost Model API without automatic inference retries.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a server-side Anyhost Model API image client.
func NewClient(config Config, options Options) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		apiKey:     config.APIKey,
		baseURL:    strings.TrimRight(config.BaseURL, "/"),
		httpClient: httpClient,
	}, nil
}

// Generate creates one billable Anyhost Generation and returns its first inline image.
func (c *Client) Generate(ctx context.Context, request Request) (*Response, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	aspectRatio := strings.TrimSpace(request.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = defaultGeneratedAspectRatio
	}

	payload := chatCompletionsRequest{
		Model: ModelID,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Modalities:  []string{"image", "text"},
		ImageConfig: imageConfig{AspectRatio: aspectRatio},
		User:        strings.TrimSpace(request.UserID),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode image generation request")
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create image generation request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send image generation request")
	}
	defer httpResponse.Body.Close()

	responseBody, err := readLimited(httpResponse.Body, maxResponseBodySizeBytes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read image generation response")
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, parseAPIError(httpResponse, responseBody)
	}

	var decoded chatCompletionsResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, errors.Wrap(err, "failed to decode image generation response")
	}
	imageURL := firstImageURL(decoded)
	if imageURL == "" {
		return nil, errors.New("image generation response did not include an image")
	}
	content, contentType, extension, err := decodeInlineImage(imageURL)
	if err != nil {
		return nil, err
	}

	generationID := strings.TrimSpace(httpResponse.Header.Get("x-anyhost-generation-id"))
	if generationID == "" {
		generationID = strings.TrimSpace(decoded.ID)
	}
	if generationID == "" {
		return nil, errors.New("image generation response did not include a Generation ID")
	}

	usage := Usage{SettlementStatus: decoded.Usage.SettlementStatus}
	if decoded.Usage.SettlementStatus == "settled" {
		usage.Cost = decoded.Usage.Cost
		usage.Credits = decoded.Usage.Credits
	}
	model := strings.TrimSpace(decoded.Model)
	if model == "" {
		model = ModelID
	}

	return &Response{
		Content:      content,
		ContentType:  contentType,
		Filename:     "nano-banana-pro." + extension,
		Model:        model,
		GenerationID: generationID,
		Usage:        usage,
	}, nil
}

type chatCompletionsRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Modalities  []string      `json:"modalities"`
	ImageConfig imageConfig   `json:"image_config"`
	User        string        `json:"user,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type imageConfig struct {
	AspectRatio string `json:"aspect_ratio"`
}

type chatCompletionsResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Images []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		SettlementStatus string `json:"settlement_status"`
		Cost             string `json:"cost"`
		Credits          string `json:"credits"`
	} `json:"usage"`
}

type apiErrorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func firstImageURL(response chatCompletionsResponse) string {
	for _, choice := range response.Choices {
		for _, image := range choice.Message.Images {
			if imageURL := strings.TrimSpace(image.ImageURL.URL); imageURL != "" {
				return imageURL
			}
		}
	}
	return ""
}

func decodeInlineImage(value string) ([]byte, string, string, error) {
	if !strings.HasPrefix(value, "data:") {
		return nil, "", "", errors.New("image generation response used an unsupported non-inline image URL")
	}
	header, encoded, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return nil, "", "", errors.New("image generation response included an invalid data URL")
	}
	parts := strings.Split(header, ";")
	contentType := strings.ToLower(strings.TrimSpace(parts[0]))
	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		return nil, "", "", errors.New("image generation response did not include base64 image data")
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil || !strings.HasPrefix(contentType, "image/") {
		return nil, "", "", errors.New("image generation response included an invalid image content type")
	}

	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		content, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, "", "", errors.Wrap(err, "failed to decode generated image")
	}
	if len(content) == 0 {
		return nil, "", "", errors.New("image generation response included an empty image")
	}
	if len(content) > maxGeneratedImageSizeBytes {
		return nil, "", "", errors.Errorf("generated image exceeds the %d-byte limit", maxGeneratedImageSizeBytes)
	}

	detectedType := http.DetectContentType(content)
	if !strings.HasPrefix(detectedType, "image/") {
		return nil, "", "", errors.New("generated content is not an image")
	}
	extension, ok := imageExtension(detectedType)
	if !ok {
		return nil, "", "", errors.Errorf("generated image content type %q is not supported", detectedType)
	}
	return content, detectedType, extension, nil
}

func imageExtension(contentType string) (string, bool) {
	switch contentType {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/webp":
		return "webp", true
	case "image/gif":
		return "gif", true
	default:
		return "", false
	}
}

func parseAPIError(response *http.Response, body []byte) error {
	var decoded apiErrorResponse
	_ = json.Unmarshal(body, &decoded)
	return &APIError{
		StatusCode: response.StatusCode,
		Code:       strings.TrimSpace(decoded.Error.Code),
		RetryAfter: strings.TrimSpace(response.Header.Get("Retry-After")),
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, errors.Errorf("response body exceeds the %d-byte limit", limit)
	}
	return content, nil
}
