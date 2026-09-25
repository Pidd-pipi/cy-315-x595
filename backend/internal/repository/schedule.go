package repository

import (
	"context"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// ScheduleFilter contains optional query filters for timetable entries.
type ScheduleFilter struct {
	Semester    string
	Week        *uint
	ClassID     *uint
	TeacherID   *uint
	ClassroomID *uint
}

// ScheduleRepository defines persistence operations for timetable entries.
type ScheduleRepository interface {
	CreateBatch(ctx context.Context, schedules []model.Schedule) error
	GetByID(ctx context.Context, id uint) (*model.Schedule, error)
	List(ctx context.Context, filter ScheduleFilter) ([]model.Schedule, error)
	Update(ctx context.Context, schedule *model.Schedule) error
	UpdateBatch(ctx context.Context, schedules []model.Schedule) error
	DeleteByID(ctx context.Context, id uint) error
	ReplaceSemester(ctx context.Context, semester string, schedules []model.Schedule) error
	LatestSemester(ctx context.Context) (string, error)
}

type scheduleRepository struct {
	db *gorm.DB
}

// NewScheduleRepository constructs a schedule repository.
func NewScheduleRepository(db *gorm.DB) ScheduleRepository {
	return &scheduleRepository{db: db}
}

func (r *scheduleRepository) CreateBatch(ctx context.Context, schedules []model.Schedule) error {
	if len(schedules) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).CreateInBatches(schedules, 200).Error; err != nil {
		return fmt.Errorf("create schedules: %w", err)
	}
	return nil
}

func (r *scheduleRepository) GetByID(ctx context.Context, id uint) (*model.Schedule, error) {
	var item model.Schedule
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}

func (r *scheduleRepository) List(ctx context.Context, filter ScheduleFilter) ([]model.Schedule, error) {
	query := r.db.WithContext(ctx).Model(&model.Schedule{})
	if filter.Semester != "" {
		query = query.Where("semester = ?", filter.Semester)
	}
	if filter.Week != nil {
		query = query.Where("week = ?", *filter.Week)
	}
	if filter.ClassID != nil {
		query = query.Where("class_id = ?", *filter.ClassID)
	}
	if filter.TeacherID != nil {
		query = query.Where("teacher_id = ?", *filter.TeacherID)
	}
	if filter.ClassroomID != nil {
		query = query.Where("classroom_id = ?", *filter.ClassroomID)
	}
	var items []model.Schedule
	if err := query.Order("week ASC, day_of_week ASC, time_slot_id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return items, nil
}

func (r *scheduleRepository) Update(ctx context.Context, schedule *model.Schedule) error {
	if err := r.db.WithContext(ctx).Save(schedule).Error; err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	return nil
}

func (r *scheduleRepository) UpdateBatch(ctx context.Context, schedules []model.Schedule) error {
	if len(schedules) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range schedules {
			if err := tx.Save(&schedules[i]).Error; err != nil {
				return fmt.Errorf("update schedules: %w", err)
			}
		}
		return nil
	})
}

func (r *scheduleRepository) DeleteByID(ctx context.Context, id uint) error {
	if err := r.db.WithContext(ctx).Delete(&model.Schedule{}, id).Error; err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

func (r *scheduleRepository) ReplaceSemester(ctx context.Context, semester string, schedules []model.Schedule) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("semester = ?", semester).Delete(&model.Schedule{}).Error; err != nil {
			return fmt.Errorf("delete semester schedules: %w", err)
		}
		if len(schedules) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(schedules, 200).Error; err != nil {
			return fmt.Errorf("create semester schedules: %w", err)
		}
		return nil
	})
}

func (r *scheduleRepository) LatestSemester(ctx context.Context) (string, error) {
	var semesters []string
	if err := r.db.WithContext(ctx).
		Model(&model.Schedule{}).
		Select("semester").
		Order("MAX(id) DESC").
		Group("semester").
		Limit(1).
		Pluck("semester", &semesters).Error; err != nil {
		return "", fmt.Errorf("query latest semester: %w", err)
	}
	if len(semesters) == 0 {
		return "", nil
	}
	return semesters[0], nil
}
