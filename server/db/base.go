package db

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

//Base is
type Base struct {
	ID        string     `sql:"type:uuid;primary_key" json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `gorm:"index" json:"deletedAt"`
}

//BeforeCreate
func (base *Base) BeforeCreate(tx *gorm.DB) error {
	tx.Statement.SetColumn("ID", uuid.NewString())
	return nil
}
