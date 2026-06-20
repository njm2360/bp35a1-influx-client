package transport

import (
	"context"
)

type Transport interface {
	Send(ctx context.Context, payload []byte) error
	Recv(ctx context.Context) ([]byte, error)
	Reconnect()
	Close() error
}
