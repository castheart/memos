package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAnyhostConfigFromEnv(t *testing.T) {
	for _, name := range []string{
		"ANYHOST_EMAIL_API_URL",
		"ANYHOST_EMAIL_TOKEN",
		"ANYHOST_WORKSPACE_ID",
		"ANYHOST_PROJECT_ID",
		"ANYHOST_ENVIRONMENT_NAME",
	} {
		t.Setenv(name, "")
	}

	config, err := LoadAnyhostConfigFromEnv()
	require.NoError(t, err)
	require.Nil(t, config)

	t.Setenv("ANYHOST_EMAIL_API_URL", "https://email.example.com")
	_, err = LoadAnyhostConfigFromEnv()
	require.ErrorContains(t, err, "ANYHOST_EMAIL_TOKEN")
	require.NotContains(t, err.Error(), "https://email.example.com")
}

func TestAnyhostClientSend(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/workspaces/wsp_test/projects/prj_test/environments/dev/email/send", r.URL.Path)
		require.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewAnyhostClient(&AnyhostConfig{
		APIURL:          server.URL,
		Token:           "secret-token",
		WorkspaceID:     "wsp_test",
		ProjectID:       "prj_test",
		EnvironmentName: "dev",
	}, server.Client())
	require.NoError(t, err)

	err = client.Send(context.Background(), &Message{
		To:      []string{"reader@example.com"},
		Subject: "A memo notification",
		Body:    "Open the memo.",
	}, "memos:inbox:42:v1", map[string]string{"message_type": "memo_comment"})
	require.NoError(t, err)
	require.Equal(t, "reader@example.com", requestBody["to"])
	require.Equal(t, "A memo notification", requestBody["subject"])
	require.Equal(t, "Open the memo.", requestBody["text"])
	require.Equal(t, "memos:inbox:42:v1", requestBody["idempotency_key"])
	require.Nil(t, requestBody["html"])
	require.Nil(t, requestBody["from"])
	require.Nil(t, requestBody["source_id"])
	require.Equal(t, "memo_comment", requestBody["metadata"].(map[string]any)["message_type"])
}

func TestAnyhostClientSendRejectsUnsupportedRecipients(t *testing.T) {
	client, err := NewAnyhostClient(&AnyhostConfig{
		APIURL:          "https://email.example.com",
		Token:           "secret-token",
		WorkspaceID:     "wsp_test",
		ProjectID:       "prj_test",
		EnvironmentName: "dev",
	}, nil)
	require.NoError(t, err)

	err = client.Send(context.Background(), &Message{
		To:      []string{"one@example.com", "two@example.com"},
		Subject: "Subject",
		Body:    "Body",
	}, "memos:test:v1", nil)
	require.ErrorContains(t, err, "exactly one recipient")
}

func TestAnyhostClientSendReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "recipient suppressed", http.StatusUnprocessableEntity)
	}))
	defer server.Close()

	client, err := NewAnyhostClient(&AnyhostConfig{
		APIURL:          server.URL,
		Token:           "secret-token",
		WorkspaceID:     "wsp_test",
		ProjectID:       "prj_test",
		EnvironmentName: "dev",
	}, server.Client())
	require.NoError(t, err)

	err = client.Send(context.Background(), &Message{
		To:      []string{"reader@example.com"},
		Subject: "Subject",
		Body:    "Body",
	}, "memos:test:v1", nil)
	require.ErrorContains(t, err, "HTTP 422")
	require.ErrorContains(t, err, "recipient suppressed")
}
