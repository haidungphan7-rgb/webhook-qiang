package start

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

func TestResolveDatabaseURL(t *testing.T) {
	t.Parallel()

	t.Run("an explicit value wins over the file", func(t *testing.T) {
		t.Parallel()

		got := resolveDatabaseURL("postgres://flag", &config.File{DatabaseURL: "postgres://file"})
		require.Equal(t, "postgres://flag", got)
	})

	t.Run("the file fills an empty value", func(t *testing.T) {
		t.Parallel()

		got := resolveDatabaseURL("", &config.File{DatabaseURL: "postgres://file"})
		require.Equal(t, "postgres://file", got)
	})

	t.Run("nil file keeps the value", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, resolveDatabaseURL("", nil))
		require.Equal(t, "postgres://flag", resolveDatabaseURL("postgres://flag", nil))
	})
}
