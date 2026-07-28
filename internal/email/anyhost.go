package email

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	anyhostEmailRequestTimeout = 15 * time.Second
	anyhostEmailErrorBodyLimit = 4 << 10
)

// AnyhostConfig contains the runtime configuration injected by Anyhost Project Email.
type AnyhostConfig struct {
	APIURL          string
	Token           string
	WorkspaceID     string
	ProjectID       string
	EnvironmentName string
}

// LoadAnyhostConfigFromEnv loads the Anyhost Project Email runtime contract.
// It returns nil when none of the Anyhost Email variables are present.
func LoadAnyhostConfigFromEnv() (*AnyhostConfig, error) {
	config := &AnyhostConfig{
		APIURL:          strings.TrimSpace(os.Getenv("ANYHOST_EMAIL_API_URL")),
		Token:           strings.TrimSpace(os.Getenv("ANYHOST_EMAIL_TOKEN")),
		WorkspaceID:     strings.TrimSpace(os.Getenv("ANYHOST_WORKSPACE_ID")),
		ProjectID:       strings.TrimSpace(os.Getenv("ANYHOST_PROJECT_ID")),
		EnvironmentName: strings.TrimSpace(os.Getenv("ANYHOST_ENVIRONMENT_NAME")),
	}
	if config.APIURL == "" &&
		config.Token == "" &&
		config.WorkspaceID == "" &&
		config.ProjectID == "" &&
		config.EnvironmentName == "" {
		return nil, nil
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// Validate checks whether all required Anyhost Project Email values are present and valid.
func (c *AnyhostConfig) Validate() error {
	if c == nil {
		return errors.New("Anyhost email configuration is required")
	}

	missing := make([]string, 0, 5)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "ANYHOST_EMAIL_API_URL", value: c.APIURL},
		{name: "ANYHOST_EMAIL_TOKEN", value: c.Token},
		{name: "ANYHOST_WORKSPACE_ID", value: c.WorkspaceID},
		{name: "ANYHOST_PROJECT_ID", value: c.ProjectID},
		{name: "ANYHOST_ENVIRONMENT_NAME", value: c.EnvironmentName},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return errors.Errorf("missing Anyhost email runtime variables: %s", strings.Join(missing, ", "))
	}

	parsedURL, err := url.ParseRequestURI(c.APIURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return errors.New("ANYHOST_EMAIL_API_URL must be an absolute HTTP(S) URL")
	}
	return nil
}

func (c *AnyhostConfig) endpoint() string {
	return strings.TrimRight(c.APIURL, "/") +
		"/api/v1/workspaces/" + url.PathEscape(c.WorkspaceID) +
		"/projects/" + url.PathEscape(c.ProjectID) +
		"/environments/" + url.PathEscape(c.EnvironmentName) +
		"/email/send"
}

type anyhostSendRequest struct {
	To             string            `json:"to"`
	Subject        string            `json:"subject"`
	HTML           string            `json:"html,omitempty"`
	Text           string            `json:"text,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// AnyhostClient sends rendered email through Anyhost Project Email.
type AnyhostClient struct {
	config     *AnyhostConfig
	httpClient *http.Client
}

// NewAnyhostClient creates an Anyhost Project Email client.
func NewAnyhostClient(config *AnyhostConfig, httpClient *http.Client) (*AnyhostClient, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: anyhostEmailRequestTimeout}
	}
	return &AnyhostClient{
		config:     config,
		httpClient: httpClient,
	}, nil
}

// Send delivers one rendered email through Anyhost Project Email.
func (c *AnyhostClient) Send(ctx context.Context, message *Message, idempotencyKey string, metadata map[string]string) error {
	if message == nil {
		return errors.New("email message is required")
	}
	if err := message.Validate(); err != nil {
		return errors.Wrap(err, "invalid email message")
	}
	if len(message.To) != 1 || len(message.Cc) != 0 || len(message.Bcc) != 0 {
		return errors.New("Anyhost email requires exactly one recipient and does not support cc or bcc")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return errors.New("Anyhost email idempotency key is required")
	}

	payload := anyhostSendRequest{
		To:             message.To[0],
		Subject:        message.Subject,
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
	}
	if message.IsHTML {
		payload.HTML = message.Body
	} else {
		payload.Text = message.Body
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "failed to encode Anyhost email request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.endpoint(), bytes.NewReader(body))
	if err != nil {
		return errors.Wrap(err, "failed to create Anyhost email request")
	}
	request.Header.Set("Authorization", "Bearer "+c.config.Token)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to send Anyhost email request")
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, anyhostEmailErrorBodyLimit))
		return nil
	}

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, anyhostEmailErrorBodyLimit))
	if readErr != nil {
		return errors.Wrapf(readErr, "Anyhost email API returned HTTP %d", response.StatusCode)
	}
	detail := strings.TrimSpace(string(responseBody))
	if detail == "" {
		return errors.Errorf("Anyhost email API returned HTTP %d", response.StatusCode)
	}
	return errors.Errorf("Anyhost email API returned HTTP %d: %s", response.StatusCode, detail)
}

type asyncAnyhostEmailRequest struct {
	config         *AnyhostConfig
	message        *Message
	idempotencyKey string
	metadata       map[string]string
}

var asyncAnyhostEmailQueue = make(chan asyncAnyhostEmailRequest, 128)

func init() {
	for range 2 {
		go func() {
			for request := range asyncAnyhostEmailQueue {
				client, err := NewAnyhostClient(request.config, nil)
				if err == nil {
					err = client.Send(context.Background(), request.message, request.idempotencyKey, request.metadata)
				}
				if err != nil {
					recipient := ""
					if request.message != nil && len(request.message.To) > 0 {
						recipient = request.message.To[0]
					}
					slog.Warn("Failed to send Anyhost email asynchronously",
						slog.String("recipient", recipient),
						slog.Any("error", err))
				}
			}
		}()
	}
}

// SendAnyhostAsync enqueues a rendered email for delivery through Anyhost Project Email.
func SendAnyhostAsync(config *AnyhostConfig, message *Message, idempotencyKey string, metadata map[string]string) {
	select {
	case asyncAnyhostEmailQueue <- asyncAnyhostEmailRequest{
		config:         config,
		message:        message,
		idempotencyKey: idempotencyKey,
		metadata:       metadata,
	}:
	default:
		slog.Warn("Dropped Anyhost email because the async queue is full")
	}
}
