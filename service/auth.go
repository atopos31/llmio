package service

import (
	"context"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

func GetAuthKey(ctx context.Context, key string) (models.AuthKey, error) {
	var resAuthKey models.AuthKey
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		authKey, err := gorm.G[models.AuthKey](tx).Where("key = ?", key).Where("status = ?", true).First(ctx)
		if err != nil {
			return err
		}
		resAuthKey = authKey

		return tx.Model(&authKey).Updates(map[string]any{
			"usage_count":  gorm.Expr("usage_count + 1"),
			"last_used_at": time.Now(),
		}).Error
	})
	return resAuthKey, err
}
