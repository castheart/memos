package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCDNMediaType(t *testing.T) {
	t.Parallel()

	require.True(t, isCDNMediaType("image/png"))
	require.True(t, isCDNMediaType("video/mp4"))
	require.False(t, isCDNMediaType("audio/mpeg"))
	require.False(t, isCDNMediaType("application/pdf"))
}
