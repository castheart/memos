package s3

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func clearAnyhostStorageEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		anyhostBucketEnv,
		anyhostPrefixEnv,
		anyhostRegionEnv,
		anyhostPublicBaseURLEnv,
		anyhostPublicKeyPrefixEnv,
		anyhostProjectIDEnv,
		anyhostEnvironmentNameEnv,
	} {
		t.Setenv(name, "")
	}
}

func TestLoadAnyhostConfig(t *testing.T) {
	clearAnyhostStorageEnvironment(t)

	runtimeConfig, err := LoadAnyhostConfig()
	require.NoError(t, err)
	require.Nil(t, runtimeConfig)

	t.Setenv(anyhostProjectIDEnv, "project")
	t.Setenv(anyhostEnvironmentNameEnv, "dev")
	t.Setenv(anyhostBucketEnv, "uploads")
	_, err = LoadAnyhostConfig()
	require.ErrorContains(t, err, anyhostPrefixEnv)

	t.Setenv(anyhostPrefixEnv, "workspace/project/dev/storage/")
	t.Setenv(anyhostRegionEnv, "us-west-2")
	t.Setenv(anyhostPublicBaseURLEnv, "https://cdn.anyhostcloud.com/workspace/project/dev/storage/")

	runtimeConfig, err = LoadAnyhostConfig()
	require.NoError(t, err)
	require.Equal(t, "uploads", runtimeConfig.Bucket)
	require.Equal(t, "workspace/project/dev/storage", runtimeConfig.Prefix)
	require.Equal(t, runtimeConfig.Prefix, runtimeConfig.PublicKeyPrefix)
}

func TestAnyhostConfigObjectKeyAndPublicURL(t *testing.T) {
	runtimeConfig := &AnyhostConfig{
		Bucket:          "uploads",
		Prefix:          "workspace/project/dev/storage",
		Region:          "us-west-2",
		PublicBaseURL:   "https://cdn.anyhostcloud.com/workspace/project/dev/storage",
		PublicKeyPrefix: "workspace/project/dev/storage",
	}

	key, err := runtimeConfig.ObjectKey("attachments/2026/07/abc_示例 图片.png")
	require.NoError(t, err)
	require.Equal(t, "workspace/project/dev/storage/attachments/2026/07/abc_示例 图片.png", key)
	require.True(t, runtimeConfig.OwnsKey(key))
	require.False(t, runtimeConfig.OwnsKey("another-project/image.png"))

	publicURL, err := runtimeConfig.PublicURL(key)
	require.NoError(t, err)
	require.Equal(t, "https://cdn.anyhostcloud.com/workspace/project/dev/storage/attachments/2026/07/abc_%E7%A4%BA%E4%BE%8B%20%E5%9B%BE%E7%89%87.png", publicURL)

	_, err = runtimeConfig.ObjectKey("../outside.png")
	require.ErrorContains(t, err, "within")
	_, err = runtimeConfig.PublicURL("another-project/image.png")
	require.ErrorContains(t, err, "outside")
}
