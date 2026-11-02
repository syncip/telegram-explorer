package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestMethodName(t *testing.T) {
	if got := method(&tg.UploadGetFileRequest{}); got != "upload.getFile" {
		t.Fatalf("method name %q", got)
	}
}
