package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScopedHasScope(t *testing.T) {
	t.Run("matches single scope", func(t *testing.T) {
		scope := Scoped{Scope: string(Websocket)}

		assert.True(t, scope.HasScope(Websocket))
		assert.False(t, scope.HasScope(FileUpload))
		assert.False(t, scope.HasScope(FileDownload))
	})

	t.Run("matches one of multiple scopes", func(t *testing.T) {
		scope := Scoped{Scope: string(FileUpload) + " " + string(FileDownload)}

		assert.True(t, scope.HasScope(FileUpload))
		assert.True(t, scope.HasScope(FileDownload))
		assert.False(t, scope.HasScope(Websocket))
	})

	t.Run("rejects missing scope claim", func(t *testing.T) {
		scope := Scoped{}

		assert.False(t, scope.HasScope(FileUpload))
		assert.False(t, scope.HasScope(Websocket))
	})
}
