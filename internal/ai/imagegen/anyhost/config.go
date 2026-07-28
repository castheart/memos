package anyhost

import (
	"net/url"
	"os"
	"strings"

	"github.com/pkg/errors"
)

// Config is the Anyhost Model API runtime contract used by image generation.
type Config struct {
	APIKey     string
	BaseURL    string
	ResourceID string
}

// LoadConfig reads the platform-managed Anyhost Model API runtime variables.
func LoadConfig() (Config, error) {
	config := Config{
		APIKey:     strings.TrimSpace(os.Getenv("ANYHOST_MODEL_API_KEY")),
		BaseURL:    strings.TrimSpace(os.Getenv("ANYHOST_MODEL_API_BASE_URL")),
		ResourceID: strings.TrimSpace(os.Getenv("ANYHOST_MODEL_API_RESOURCE_ID")),
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks that every value in the runtime contract is present and usable.
func (c Config) Validate() error {
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "ANYHOST_MODEL_API_KEY", value: c.APIKey},
		{name: "ANYHOST_MODEL_API_BASE_URL", value: c.BaseURL},
		{name: "ANYHOST_MODEL_API_RESOURCE_ID", value: c.ResourceID},
	} {
		if strings.TrimSpace(item.value) == "" {
			return errors.Errorf("%s is required", item.name)
		}
	}

	parsed, err := url.Parse(c.BaseURL)
	if err != nil {
		return errors.Wrap(err, "invalid ANYHOST_MODEL_API_BASE_URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("ANYHOST_MODEL_API_BASE_URL must be an absolute HTTP(S) URL")
	}
	if parsed.Host == "" {
		return errors.New("ANYHOST_MODEL_API_BASE_URL must include a host")
	}
	return nil
}
