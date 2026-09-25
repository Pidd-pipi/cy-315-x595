package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
)

// ScheduleService exposes scheduling, conflict detection, adjustment and statistics operations.
type ScheduleService interface {
	Generate(ctx context.Context, req *dto.GenerateScheduleRequest) (*dto.GenerateScheduleResponse, error)
	List(ctx context.Context, semester *string, week, classID, teacherID, classroomID *uint) ([]dto.ScheduleResponse, error)
	ListSemesters(ctx context.Context) ([]string, error)
	Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error)
	CheckConflicts(ctx context.Context, semester *string) ([]dto.ConflictResponse, error)
	Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error)
	Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error)
	ListAdjustments(ctx context.Context, semester *string, page, pageSize int) ([]dto.AdjustmentLogResponse, int64, error)
	ClassroomUtilization(ctx context.Context, semester *string) ([]dto.ClassroomUtilizationItem, error)
	TeacherWorkload(ctx context.Context, semester *string) ([]dto.TeacherWorkloadItem, error)
	CourseDensity(ctx context.Context, semester *string) ([]dto.CourseDensityItem, error)
}

type scheduleService struct {
	schedules   repository.ScheduleRepository
	semesters   repository.SemesterRepository
	classrooms  repository.ClassroomRepository
	teachers    repository.TeacherRepository
	classes     repository.ClassRepository
	courses     repository.CourseRepository
	timeSlots   repository.TimeSlotRepository
	adjustments repository.AdjustmentLogRepository
	logger      *slog.Logger
}

// NewScheduleService constructs a schedule service.
func NewScheduleService(
	schedules repository.ScheduleRepository,
	semesters repository.SemesterRepository,
	classrooms repository.ClassroomRepository,
	teachers repository.TeacherRepository,
	classes repository.ClassRepository,
	courses repository.CourseRepository,
	timeSlots repository.TimeSlotRepository,
	adjustments repository.AdjustmentLogRepository,
	logger *slog.Logger,
) ScheduleService {
	return &scheduleService{
		schedules:   schedules,
		semesters:   semesters,
		classrooms:  classrooms,
		teachers:    teachers,
		classes:     classes,
		courses:     courses,
		timeSlots:   timeSlots,
		adjustments: adjustments,
		logger:      logger,
	}
}

// resolveSemester returns the semester to operate on. An explicitly given
// semester (after trimming whitespace) is used as-is; otherwise the semester
// generated most recently is used. When no semester has ever been generated,
// an empty string is returned without error.
func (s *scheduleService) resolveSemester(ctx context.Context, requested string) (string, error) {
	if semester := strings.TrimSpace(requested); semester != "" {
		return semester, nil
	}
	latest, err := s.semesters.Latest(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve latest semester: %w", err)
	}
	return latest, nil
}

// Generate creates a timetable with a greedy scheduling algorithm.
func (s *scheduleService) Generate(ctx context.Context, req *dto.GenerateScheduleRequest) (*dto.GenerateScheduleResponse, error) {
	semester := strings.TrimSpace(req.Semester)
	if semester == "" {
		return nil, fmt.Errorf("generate schedule: %w: semester must not be empty", ErrInvalid)
	}

	allSlots, _, err := s.timeSlots.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("load time slots: %w", err)
	}
	if req.PeriodsPerDay > len(allSlots) {
		return nil, fmt.Errorf("generate schedule: %w: periods_per_day %d exceeds available time slots %d", ErrInvalid, req.PeriodsPerDay, len(allSlots))
	}
	slots := allSlots[:req.PeriodsPerDay]

	courses, err := s.courses.GetByIDs(ctx, requirementCourseIDs(req.Courses))
	if err != nil {
		return nil, fmt.Errorf("load courses: %w", err)
	}
	courseMap := entityMap(courses, func(c model.Course) uint { return c.ID })

	classes, err := s.resolveClasses(ctx, req)
	if err != nil {
		return nil, err
	}
	teachers, err := s.resolveTeachers(ctx, req)
	if err != nil {
		return nil, err
	}
	classrooms, err := s.resolveClassrooms(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(teachers) == 0 || len(classes) == 0 || len(classrooms) == 0 {
		return nil, fmt.Errorf("generate schedule: %w: teachers, classes and classrooms must not be empty", ErrInvalid)
	}

	teacherCursor := 0
	var allSchedules []model.Schedule
	var conflicts []dto.ConflictResponse
	required := 0

	for week := 1; week <= req.Weeks; week++ {
		occ := newOccupancy()
		for _, requirement := range req.Courses {
			targetClasses := targetClassesForRequirement(classes, requirement, req.ClassIDs)
			for _, class := range targetClasses {
				teacher := pickTeacher(teachers, requirement, courses, courseMap, &teacherCursor)
				if teacher == nil {
					continue
				}
				course, ok := courseMap[requirement.CourseID]
				if !ok {
					conflicts = append(conflicts, dto.ConflictResponse{
						Type: constants.ConflictTeacherTime, Semester: semester, EntityType: "course", EntityID: requirement.CourseID,
						EntityName: fmt.Sprintf("course %d", requirement.CourseID), Week: uint(week),
						Suggestion: "course not found",
					})
					continue
				}
				required += requirement.WeeklyPeriods

				positions := buildCandidatePositions(req.DaysPerWeek, slots, requirement.Consecutive, requirement.WeeklyPeriods)
				chosen, chosenClassrooms, ok := placeGreedy(uint(week), positions, requirement.WeeklyPeriods, occ, class, *teacher, course, classrooms, slots)
				if !ok {
					conflicts = append(conflicts, dto.ConflictResponse{
						Type:       constants.ConflictTeacherTime,
						Semester:   semester,
						EntityType: "course",
						EntityID:   requirement.CourseID,
						EntityName: course.Name,
						Week:       uint(week),
						Suggestion: fmt.Sprintf("unable to place %d periods for course %s in week %d; add teachers/classrooms or relax constraints", requirement.WeeklyPeriods, course.Name, week),
					})
					continue
				}
				for i := range chosen {
					allSchedules = append(allSchedules, model.Schedule{
						Semester:    semester,
						Week:        uint(week),
						DayOfWeek:   chosen[i].Day,
						TimeSlotID:  chosen[i].Slot.ID,
						ClassroomID: chosenClassrooms[i].ID,
						TeacherID:   teacher.ID,
						ClassID:     class.ID,
						CourseID:    course.ID,
					})
				}
			}
		}
	}

	// Replace only the requested semester so timetable history of other
	// semesters stays intact. The delete and insert run in one transaction.
	if err := s.schedules.ReplaceBySemester(ctx, semester, allSchedules); err != nil {
		return nil, fmt.Errorf("replace schedules for semester: %w", err)
	}
	if err := s.semesters.TouchGenerated(ctx, semester, time.Now()); err != nil {
		return nil, fmt.Errorf("record semester generation: %w", err)
	}

	generatedConflicts, err := s.checkConflicts(ctx, semester)
	if err != nil {
		return nil, fmt.Errorf("check generated conflicts: %w", err)
	}

	responses, err := s.enrichSchedules(ctx, allSchedules)
	if err != nil {
		return nil, fmt.Errorf("enrich schedules: %w", err)
	}
	resp := &dto.GenerateScheduleResponse{
		Semester:  semester,
		Schedules: responses,
		Conflicts: append(conflicts, generatedConflicts...),
		Generated: len(allSchedules),
		Required:  required,
	}
	return resp, nil
}

func (s *scheduleService) List(ctx context.Context, semester *string, week, classID, teacherID, classroomID *uint) ([]dto.ScheduleResponse, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return []dto.ScheduleResponse{}, nil
	}
	filter := repository.ScheduleFilter{Semester: &resolved, Week: week, ClassID: classID, TeacherID: teacherID, ClassroomID: classroomID}
	items, err := s.schedules.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return s.enrichSchedules(ctx, items)
}

// ListSemesters returns semester names ordered by the most recent generation first.
func (s *scheduleService) ListSemesters(ctx context.Context) ([]string, error) {
	out, err := s.semesters.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list semesters: %w", err)
	}
	return out, nil
}

func (s *scheduleService) Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error) {
	item, err := s.schedules.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*item})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 {
		return nil, ErrNotFound
	}
	return &responses[0], nil
}

func (s *scheduleService) CheckConflicts(ctx context.Context, semester *string) ([]dto.ConflictResponse, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return []dto.ConflictResponse{}, nil
	}
	return s.checkConflicts(ctx, resolved)
}

func (s *scheduleService) checkConflicts(ctx context.Context, semester string) ([]dto.ConflictResponse, error) {
	items, err := s.schedules.List(ctx, repository.ScheduleFilter{Semester: &semester})
	if err != nil {
		return nil, fmt.Errorf("list schedules for conflict check: %w", err)
	}
	return s.detectConflicts(ctx, items), nil
}

func (s *scheduleService) Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error) {
	a, err := s.schedules.GetByID(ctx, req.ScheduleAID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule a: %w", err)
	}
	b, err := s.schedules.GetByID(ctx, req.ScheduleBID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule b: %w", err)
	}
	if a.Semester != b.Semester {
		return nil, fmt.Errorf("swap schedules: %w: lessons belong to different semesters (%s and %s); cross-semester swaps are not allowed", ErrInvalid, a.Semester, b.Semester)
	}
	a.Week, b.Week = b.Week, a.Week
	a.DayOfWeek, b.DayOfWeek = b.DayOfWeek, a.DayOfWeek
	a.TimeSlotID, b.TimeSlotID = b.TimeSlotID, a.TimeSlotID
	a.ClassroomID, b.ClassroomID = b.ClassroomID, a.ClassroomID
	if err := s.schedules.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("update schedule a: %w", err)
	}
	if err := s.schedules.Update(ctx, b); err != nil {
		return nil, fmt.Errorf("update schedule b: %w", err)
	}
	logID, err := s.recordAdjustment(ctx, a.Semester, req.ScheduleAID, constants.ActionSwap, map[string]any{"schedule_a_id": req.ScheduleAID, "schedule_b_id": req.ScheduleBID})
	if err != nil {
		return nil, err
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*a})
	if err != nil {
		return nil, err
	}
	conflicts, err := s.checkConflicts(ctx, a.Semester)
	if err != nil {
		return nil, err
	}
	return &dto.AdjustmentResponse{Schedule: responses[0], Conflicts: conflicts, LogID: logID}, nil
}

func (s *scheduleService) Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error) {
	item, err := s.schedules.GetByID(ctx, req.ScheduleID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	// Lessons are always moved within their own semester; moving a lesson to
	// another semester is rejected before any data is changed.
	if target := strings.TrimSpace(req.Semester); target != "" && target != item.Semester {
		return nil, fmt.Errorf("move schedule: %w: cannot move lesson from semester %q to semester %q; create the lesson in the target semester instead", ErrInvalid, item.Semester, target)
	}
	item.Week = req.Week
	item.DayOfWeek = req.DayOfWeek
	item.TimeSlotID = req.TimeSlotID
	item.ClassroomID = req.ClassroomID
	if err := s.schedules.Update(ctx, item); err != nil {
		return nil, fmt.Errorf("update schedule: %w", err)
	}
	logID, err := s.recordAdjustment(ctx, item.Semester, req.ScheduleID, constants.ActionMove, map[string]any{"week": req.Week, "day_of_week": req.DayOfWeek, "time_slot_id": req.TimeSlotID, "classroom_id": req.ClassroomID})
	if err != nil {
		return nil, err
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*item})
	if err != nil {
		return nil, err
	}
	conflicts, err := s.checkConflicts(ctx, item.Semester)
	if err != nil {
		return nil, err
	}
	return &dto.AdjustmentResponse{Schedule: responses[0], Conflicts: conflicts, LogID: logID}, nil
}

func (s *scheduleService) ListAdjustments(ctx context.Context, semester *string, page, pageSize int) ([]dto.AdjustmentLogResponse, int64, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, 0, err
	}
	if resolved == "" {
		return []dto.AdjustmentLogResponse{}, 0, nil
	}
	items, total, err := s.adjustments.List(ctx, repository.AdjustmentLogFilter{Semester: &resolved}, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list adjustment logs: %w", err)
	}
	out := make([]dto.AdjustmentLogResponse, 0, len(items))
	for i := range items {
		out = append(out, dto.AdjustmentLogResponse{
			ID:         items[i].ID,
			Semester:   items[i].Semester,
			ScheduleID: items[i].ScheduleID,
			Action:     items[i].Action,
			Detail:     items[i].Detail,
			CreatedAt:  items[i].CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, total, nil
}

func (s *scheduleService) ClassroomUtilization(ctx context.Context, semester *string) ([]dto.ClassroomUtilizationItem, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, err
	}
	classrooms, _, err := s.classrooms.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list classrooms: %w", err)
	}
	if resolved == "" {
		out := make([]dto.ClassroomUtilizationItem, 0, len(classrooms))
		for i := range classrooms {
			out = append(out, dto.ClassroomUtilizationItem{ClassroomID: classrooms[i].ID, ClassroomName: classrooms[i].Name})
		}
		return out, nil
	}
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{Semester: &resolved})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	weeks := distinctWeeks(schedules)
	days := maxDayOfWeek(schedules)
	slots, _, err := s.timeSlots.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list time slots: %w", err)
	}
	totalPeriods := int64(len(weeks)) * int64(days) * int64(len(slots))
	usedByClassroom := map[uint]int64{}
	for _, item := range schedules {
		usedByClassroom[item.ClassroomID]++
	}
	out := make([]dto.ClassroomUtilizationItem, 0, len(classrooms))
	for i := range classrooms {
		used := usedByClassroom[classrooms[i].ID]
		utilization := float64(0)
		if totalPeriods > 0 {
			utilization = float64(used) / float64(totalPeriods)
		}
		out = append(out, dto.ClassroomUtilizationItem{
			ClassroomID:   classrooms[i].ID,
			ClassroomName: classrooms[i].Name,
			UsedPeriods:   used,
			TotalPeriods:  totalPeriods,
			Utilization:   utilization,
		})
	}
	return out, nil
}

func (s *scheduleService) TeacherWorkload(ctx context.Context, semester *string) ([]dto.TeacherWorkloadItem, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, err
	}
	teachers, _, err := s.teachers.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list teachers: %w", err)
	}
	out := make([]dto.TeacherWorkloadItem, 0, len(teachers))
	if resolved == "" {
		for i := range teachers {
			out = append(out, dto.TeacherWorkloadItem{TeacherID: teachers[i].ID, TeacherName: teachers[i].Name})
		}
		return out, nil
	}
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{Semester: &resolved})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	counts := map[uint]int64{}
	for _, item := range schedules {
		counts[item.TeacherID]++
	}
	for i := range teachers {
		out = append(out, dto.TeacherWorkloadItem{TeacherID: teachers[i].ID, TeacherName: teachers[i].Name, Periods: counts[teachers[i].ID]})
	}
	return out, nil
}

func (s *scheduleService) CourseDensity(ctx context.Context, semester *string) ([]dto.CourseDensityItem, error) {
	resolved, err := s.resolveSemester(ctx, derefString(semester))
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return []dto.CourseDensityItem{}, nil
	}
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{Semester: &resolved})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	type key struct {
		day  int
		slot uint
	}
	counts := map[key]int64{}
	for _, item := range schedules {
		k := key{day: item.DayOfWeek, slot: item.TimeSlotID}
		counts[k]++
	}
	out := make([]dto.CourseDensityItem, 0, len(counts))
	for k, v := range counts {
		out = append(out, dto.CourseDensityItem{DayOfWeek: k.day, TimeSlotID: k.slot, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DayOfWeek != out[j].DayOfWeek {
			return out[i].DayOfWeek < out[j].DayOfWeek
		}
		return out[i].TimeSlotID < out[j].TimeSlotID
	})
	return out, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *scheduleService) recordAdjustment(ctx context.Context, semester string, scheduleID uint, action string, detail any) (uint, error) {
	data, err := json.Marshal(detail)
	if err != nil {
		return 0, fmt.Errorf("marshal adjustment detail: %w", err)
	}
	log := &model.AdjustmentLog{Semester: semester, ScheduleID: scheduleID, Action: action, Detail: string(data)}
	if err := s.adjustments.Create(ctx, log); err != nil {
		return 0, fmt.Errorf("record adjustment: %w", err)
	}
	return log.ID, nil
}
