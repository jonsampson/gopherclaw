// Self-registration: when MATRIX_HOMESERVER, MATRIX_USER_ID, and
// MATRIX_ACCESS_TOKEN are all present in the environment, this file's init()
// registers the Matrix adapter with the channels registry at startup.
// If any variable is absent the adapter is silently skipped so the binary
// remains functional without Matrix credentials.
package matrix

import (
	"os"

	"github.com/jonsampson/gopherclaw/internal/channels"
	"github.com/jonsampson/gopherclaw/internal/types"
)

func init() {
	hs := os.Getenv("MATRIX_HOMESERVER")
	uid := os.Getenv("MATRIX_USER_ID")
	tok := os.Getenv("MATRIX_ACCESS_TOKEN")
	if hs == "" || uid == "" || tok == "" {
		return
	}
	channels.Register("matrix", func(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) types.Channel {
		return New(hs, uid, tok, onMsg, onMeta)
	})
}
