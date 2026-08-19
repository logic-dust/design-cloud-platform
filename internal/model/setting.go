package model

type Setting struct {
	Domain string `gorm:"primaryKey"`
	Value  string `gorm:"not null;type:text"`
}
