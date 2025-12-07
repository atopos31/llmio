package service

import (
	"context"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

func GetAuthKey(ctx context.Context, key string) (models.AuthKey, error) {
	return gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Where("status = ?", true).First(ctx)
}
