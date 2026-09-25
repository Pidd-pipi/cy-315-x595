package model

import "gorm.io/gorm"

// AdjustmentLog records a manual timetable change for audit/history purposes.
type AdjustmentLog struct {
	gorm.Model
	Semester   string `gorm:"index;not null;default:'未命名学期'" json:"semester"`
	ScheduleID uint   `gorm:"index" json:"schedule_id"`
	Action     string `gorm:"size:32;not null" json:"action"`
	Detail     string `gorm:"type:text" json:"detail"`
}
