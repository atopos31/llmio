package service

import (
	"context"

	"github.com/atopos31/llmio/models"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

func ModelsByTypes(ctx context.Context, modelTypes ...string) ([]models.Model, error) {
	llmproviders, err := gorm.G[models.Provider](models.DB).Where("type IN ?", modelTypes).Find(ctx)
	if err != nil {
		return nil, err
	}

	modelWithProviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_id IN ?", lo.Map(llmproviders, func(p models.Provider, _ int) uint { return p.ID })).Find(ctx)
	if err != nil {
		return nil, err
	}

	models, err := gorm.G[models.Model](models.DB).Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ModelID })).Find(ctx)
	if err != nil {
		return nil, err
	}
	return models, nil
}
