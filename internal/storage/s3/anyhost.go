package s3

import (
	"context"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pkg/errors"

	storepb "github.com/usememos/memos/proto/gen/store"
)

const (
	anyhostBucketEnv          = "S3_BUCKET"
	anyhostPrefixEnv          = "S3_PREFIX"
	anyhostRegionEnv          = "S3_REGION"
	anyhostPublicBaseURLEnv   = "S3_PUBLIC_BASE_URL"
	anyhostPublicKeyPrefixEnv = "S3_PUBLIC_KEY_PREFIX"
	anyhostProjectIDEnv       = "ANYHOST_PROJECT_ID"
	anyhostEnvironmentNameEnv = "ANYHOST_ENVIRONMENT_NAME"
)

// AnyhostConfig is the platform-managed object storage runtime contract.
type AnyhostConfig struct {
	Bucket          string
	Prefix          string
	Region          string
	PublicBaseURL   string
	PublicKeyPrefix string
}

// LoadAnyhostConfig reads Anyhost's generated S3 runtime variables. It returns
// nil when the runtime contract is entirely absent, allowing non-Anyhost
// deployments to continue using the instance storage setting.
func LoadAnyhostConfig() (*AnyhostConfig, error) {
	if strings.TrimSpace(os.Getenv(anyhostProjectIDEnv)) == "" || strings.TrimSpace(os.Getenv(anyhostEnvironmentNameEnv)) == "" {
		return nil, nil
	}

	runtimeConfig := &AnyhostConfig{
		Bucket:          strings.TrimSpace(os.Getenv(anyhostBucketEnv)),
		Prefix:          strings.Trim(strings.TrimSpace(os.Getenv(anyhostPrefixEnv)), "/"),
		Region:          strings.TrimSpace(os.Getenv(anyhostRegionEnv)),
		PublicBaseURL:   strings.TrimRight(strings.TrimSpace(os.Getenv(anyhostPublicBaseURLEnv)), "/"),
		PublicKeyPrefix: strings.Trim(strings.TrimSpace(os.Getenv(anyhostPublicKeyPrefixEnv)), "/"),
	}

	if runtimeConfig.Bucket == "" &&
		runtimeConfig.Prefix == "" &&
		runtimeConfig.Region == "" &&
		runtimeConfig.PublicBaseURL == "" &&
		runtimeConfig.PublicKeyPrefix == "" {
		return nil, nil
	}

	for _, item := range []struct {
		name  string
		value string
	}{
		{name: anyhostBucketEnv, value: runtimeConfig.Bucket},
		{name: anyhostPrefixEnv, value: runtimeConfig.Prefix},
		{name: anyhostRegionEnv, value: runtimeConfig.Region},
		{name: anyhostPublicBaseURLEnv, value: runtimeConfig.PublicBaseURL},
	} {
		if item.value == "" {
			return nil, errors.Errorf("%s is required when Anyhost Storage is configured", item.name)
		}
	}

	publicBaseURL, err := url.Parse(runtimeConfig.PublicBaseURL)
	if err != nil {
		return nil, errors.Wrap(err, "invalid S3_PUBLIC_BASE_URL")
	}
	if publicBaseURL.Scheme != "https" || publicBaseURL.Host == "" {
		return nil, errors.New("S3_PUBLIC_BASE_URL must be an absolute HTTPS URL")
	}
	if publicBaseURL.RawQuery != "" || publicBaseURL.Fragment != "" {
		return nil, errors.New("S3_PUBLIC_BASE_URL must not include a query or fragment")
	}

	if runtimeConfig.PublicKeyPrefix == "" {
		runtimeConfig.PublicKeyPrefix = runtimeConfig.Prefix
	}
	return runtimeConfig, nil
}

// ObjectKey scopes a relative object path to the Environment Storage prefix.
func (c *AnyhostConfig) ObjectKey(relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" || strings.HasPrefix(relativePath, "/") {
		return "", errors.New("object path must be relative")
	}
	cleaned := path.Clean(relativePath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("object path must stay within the Storage prefix")
	}
	return path.Join(c.Prefix, cleaned), nil
}

// OwnsKey reports whether an object key belongs to this Environment Storage
// prefix.
func (c *AnyhostConfig) OwnsKey(key string) bool {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefix := strings.Trim(c.Prefix, "/")
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

// PublicURL converts a Storage object key into its stable Anyhost CDN URL.
func (c *AnyhostConfig) PublicURL(key string) (string, error) {
	key = strings.Trim(strings.TrimSpace(key), "/")
	publicKeyPrefix := strings.Trim(c.PublicKeyPrefix, "/")
	if key != publicKeyPrefix && !strings.HasPrefix(key, publicKeyPrefix+"/") {
		return "", errors.New("object key is outside S3_PUBLIC_KEY_PREFIX")
	}
	relativePath := strings.TrimPrefix(strings.TrimPrefix(key, publicKeyPrefix), "/")
	if relativePath == "" {
		return c.PublicBaseURL, nil
	}
	publicURL, err := url.JoinPath(c.PublicBaseURL, relativePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to build Anyhost CDN URL")
	}
	return publicURL, nil
}

// NewAnyhostClient creates an S3 client with the ECS task's default credential
// provider chain. Anyhost does not inject long-lived access keys.
func NewAnyhostClient(ctx context.Context, runtimeConfig *AnyhostConfig) (*Client, error) {
	if runtimeConfig == nil {
		return nil, errors.New("Anyhost Storage config is required")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(runtimeConfig.Region))
	if err != nil {
		return nil, errors.Wrap(err, "failed to load Anyhost S3 config")
	}
	client := awss3.NewFromConfig(cfg, func(options *awss3.Options) {
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		options.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Client{
		Client: client,
		Bucket: aws.String(runtimeConfig.Bucket),
	}, nil
}

// LoadAnyhostConfigForKey returns the Anyhost runtime config only when the key
// belongs to its Environment-scoped prefix.
func LoadAnyhostConfigForKey(key string) (*AnyhostConfig, error) {
	runtimeConfig, err := LoadAnyhostConfig()
	if err != nil {
		return nil, err
	}
	if runtimeConfig == nil || !runtimeConfig.OwnsKey(key) {
		return nil, nil
	}
	return runtimeConfig, nil
}

// NewClientForObject selects static instance credentials for legacy objects or
// the ECS task-role credential chain for Anyhost-managed objects.
func NewClientForObject(ctx context.Context, objectConfig *storepb.StorageS3Config, key string) (*Client, error) {
	if objectConfig != nil {
		return NewClient(ctx, objectConfig)
	}
	runtimeConfig, err := LoadAnyhostConfigForKey(key)
	if err != nil {
		return nil, err
	}
	if runtimeConfig == nil {
		return nil, errors.New("S3 config is missing and the object is outside Anyhost Storage")
	}
	return NewAnyhostClient(ctx, runtimeConfig)
}
