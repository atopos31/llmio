package models

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Init(name string) {
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	DB = db
	if err := db.AutoMigrate(&Provider{}, &Model{}, &ModelWithProvider{}, &ChatLog{}); err != nil {
		panic(err)
	}
}
