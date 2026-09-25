package model

import (
	"time"

	"gorm.io/gorm"
)

// Semester records a timetable term and the time of its most recent
// generation run. It is used to resolve the default (latest) semester when
// callers do not specify one explicitly.
type Semester struct {
	gorm.Model
	Name            string     `gorm:"size:128;uniqueIndex;not null" json:"name"`
	LastGeneratedAt *time.Time `gorm:"index" json:"last_generated_at"`
}

// TableName explicitly names the table to avoid GORM's plural convention.
func (Semester) TableName() string { return "semesters" }
