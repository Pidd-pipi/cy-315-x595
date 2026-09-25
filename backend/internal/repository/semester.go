package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// SemesterRepository persists semester metadata.
type SemesterRepository interface {
	// TouchGenerated records that a timetable was generated for the named
	// semester at the given time, creating the semester when necessary.
	TouchGenerated(ctx context.Context, name string, at time.Time) error
	// Latest returns the name of the semester generated most recently. It
	// returns an empty name without an error when no semester exists yet.
	Latest(ctx context.Context) (string, error)
	// List returns semester names ordered by the latest generation first.
	List(ctx context.Context) ([]string, error)
}

type semesterRepository struct {
	db *gorm.DB
}

// NewSemesterRepository constructs a semester repository.
func NewSemesterRepository(db *gorm.DB) SemesterRepository {
	return &semesterRepository{db: db}
}

func (r *semesterRepository) TouchGenerated(ctx context.Context, name string, at time.Time) error {
	var semester model.Semester
	// A not-found result is an expected branch when a semester is generated
	// for the first time, so use Limit(1).Find to avoid surfacing it as a
	// logged query error.
	result := r.db.WithContext(ctx).Where("name = ?", name).Limit(1).Find(&semester)
	if result.Error != nil {
		return fmt.Errorf("load semester: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		semester.LastGeneratedAt = &at
		if err := r.db.WithContext(ctx).Save(&semester).Error; err != nil {
			return fmt.Errorf("touch semester: %w", err)
		}
		return nil
	}
	semester = model.Semester{Name: name, LastGeneratedAt: &at}
	if err := r.db.WithContext(ctx).Create(&semester).Error; err != nil {
		if isConstraintError(err) {
			// A concurrent request may have inserted the same semester.
			if updErr := r.db.WithContext(ctx).Model(&model.Semester{}).
				Where("name = ?", name).Update("last_generated_at", at).Error; updErr != nil {
				return fmt.Errorf("touch semester after conflict: %w", updErr)
			}
			return nil
		}
		return fmt.Errorf("create semester: %w", err)
	}
	return nil
}

func (r *semesterRepository) Latest(ctx context.Context) (string, error) {
	var semester model.Semester
	result := r.db.WithContext(ctx).
		Where("last_generated_at IS NOT NULL").
		Order("last_generated_at DESC, id DESC").Limit(1).Find(&semester)
	if result.Error != nil {
		return "", fmt.Errorf("load latest semester: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return "", nil
	}
	return semester.Name, nil
}

func (r *semesterRepository) List(ctx context.Context) ([]string, error) {
	var semesters []model.Semester
	if err := r.db.WithContext(ctx).
		Where("last_generated_at IS NOT NULL").
		Order("last_generated_at DESC, id DESC").Find(&semesters).Error; err != nil {
		return nil, fmt.Errorf("list semesters: %w", err)
	}
	out := make([]string, 0, len(semesters))
	for i := range semesters {
		out = append(out, semesters[i].Name)
	}
	return out, nil
}
