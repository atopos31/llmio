package prividers

import (
	"context"
	"io"
)

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"` // 使用 int64 存储 Unix 时间戳
	OwnedBy string `json:"owned_by"`
}

type Privider interface {
	Chat(ctx context.Context, rawData []byte) (body io.ReadCloser, status int, err error)
	Models(ctx context.Context) ([]Model, error)
}
