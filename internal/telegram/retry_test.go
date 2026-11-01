package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type invokerFunc func(ctx context.Context, input bin.Encoder, output bin.Decoder) error

func (f invokerFunc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}

func TestTransferRetry(t *testing.T) {
	dropped := errors.New("engine forcibly closed: context canceled")
	ctx := context.Background()
	part := &tg.UploadSaveBigFilePartRequest{}

	run := func(ctx context.Context, input bin.Encoder, fails int, failure error) (int, int, error) {
		calls, notified := 0, 0
		retry := transferRetry{notify: func(error) { notified++ }, delay: time.Millisecond}
		err := retry.Handle(invokerFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			if calls++; calls <= fails {
				return failure
			}
			return nil
		}))(ctx, input, nil)
		return calls, notified, err
	}

	if calls, notified, err := run(ctx, part, 2, dropped); err != nil || calls != 3 || notified != 1 {
		t.Fatalf("dropped connection: %d calls, %d notes, %v", calls, notified, err)
	}
	if calls, _, err := run(ctx, &tg.UploadGetFileRequest{}, 1, dropped); err != nil || calls != 2 {
		t.Fatalf("download: %d calls, %v", calls, err)
	}
	if calls, _, err := run(ctx, part, 1000, dropped); err == nil || calls != transferRetries+1 {
		t.Fatalf("gives up: %d calls, %v", calls, err)
	}
	if calls, _, err := run(ctx, &tg.ChannelsDeleteMessagesRequest{}, 1, dropped); err == nil || calls != 1 {
		t.Fatalf("other requests must not repeat: %d calls", calls)
	}
	if calls, _, err := run(ctx, part, 1, tgerr.New(400, "FILE_PART_INVALID")); err == nil || calls != 1 {
		t.Fatalf("answers from Telegram must not repeat: %d calls", calls)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if calls, _, err := run(stopped, part, 1, dropped); err == nil || calls != 1 {
		t.Fatalf("canceled by us: %d calls", calls)
	}
}
