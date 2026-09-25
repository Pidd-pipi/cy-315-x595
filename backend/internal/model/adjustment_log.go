package model

import "gorm.io/gorm"

// AdjustmentLog records a manual timetable change for audit/history purposes.
type AdjustmentLog struct {
	gorm.Model
	Semester   string `gorm:"size:128;index;not null;default:''" json:"semester"`
	ScheduleID uint   `gorm:"index" json:"schedule_id"`
	Action     string `gorm:"size:32;not null" json:"action"`
	Detail     string `gorm:"type:text" json:"detail"`
}
