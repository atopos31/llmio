package model

import (
	"encoding/json"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB(name string) {
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	DB = db
	if err := db.AutoMigrate(&Provider{}); err != nil {
		panic(err)
	}
	deepseekConfig := &OpenAIConfig{
		ApiKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseUrl: os.Getenv("DEEPSEEK_BASE_URL"),
	}
	data, err := json.Marshal(deepseekConfig)
	if err != nil {
		panic(err)
	}
	if err := db.FirstOrCreate(&Provider{}, &Provider{
		Name:   "deepseek",
		Type:   "openai",
		Config: string(data)}).Error; err != nil {
		panic(err)
	}
	flowConfig := &OpenAIConfig{
		ApiKey:  os.Getenv("FLOW_API_KEY"),
		BaseUrl: os.Getenv("FLOW_BASE_URL"),
	}
	data, err = json.Marshal(flowConfig)
	if err != nil {
		panic(err)
	}
	if err := db.FirstOrCreate(&Provider{}, &Provider{
		Name:   "flow",
		Type:   "openai",
		Config: string(data)}).Error; err != nil {
		panic(err)
	}
}
