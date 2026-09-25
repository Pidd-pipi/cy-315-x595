package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
	"github.com/gbschedule/gbschedule/internal/service"
)

func newScheduleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Classroom{}, &model.Teacher{}, &model.Class{}, &model.Course{}, &model.TimeSlot{}, &model.Semester{}, &model.Schedule{}, &model.AdjustmentLog{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func newScheduleService(t *testing.T, db *gorm.DB) service.ScheduleService {
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	return service.NewScheduleService(
		repository.NewScheduleRepository(db),
		repository.NewSemesterRepository(db),
		repository.NewClassroomRepository(db),
		repository.NewTeacherRepository(db),
		repository.NewClassRepository(db),
		repository.NewCourseRepository(db),
		repository.NewTimeSlotRepository(db),
		repository.NewAdjustmentLogRepository(db),
		logger,
	)
}

func TestScheduleServiceDetectConflicts(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot1 := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	slot2 := &model.TimeSlot{Code: "2", Name: "第二节", StartTime: "10:00", EndTime: "11:40"}
	if err := db.Create(slot1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(slot2).Error; err != nil {
		t.Fatal(err)
	}

	smallRoom := &model.Classroom{Code: "R301", Name: "小教室", Capacity: 30}
	bigRoom := &model.Classroom{Code: "R302", Name: "大教室", Capacity: 50}
	if err := db.Create(smallRoom).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(bigRoom).Error; err != nil {
		t.Fatal(err)
	}

	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}, UnavailableSlots: []string{"1"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	schedules := []model.Schedule{
		{Semester: "2024-2025-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID, ClassroomID: smallRoom.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID},
		{Semester: "2024-2025-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID, ClassroomID: bigRoom.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	semester := "2024-2025-1"
	conflicts, err := svc.CheckConflicts(ctx, &semester)
	if err != nil {
		t.Fatalf("check conflicts: %v", err)
	}

	wantTypes := []string{
		constants.ConflictTeacherTime,
		constants.ConflictClassTime,
		constants.ConflictClassroomCap,
		constants.ConflictTeacherPref,
	}
	gotTypes := map[string]bool{}
	for _, c := range conflicts {
		gotTypes[c.Type] = true
	}
	for _, want := range wantTypes {
		if !gotTypes[want] {
			t.Errorf("missing conflict type %s in %v", want, conflicts)
		}
	}
}

func TestScheduleServiceGenerateAllowsParallelClasses(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room1 := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	room2 := &model.Classroom{Code: "R302", Name: "302教室", Capacity: 50}
	if err := db.Create(room1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(room2).Error; err != nil {
		t.Fatal(err)
	}
	teacher1 := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	teacher2 := &model.Teacher{Name: "李老师", EmployeeNo: "T002", Subjects: []string{"语文"}}
	if err := db.Create(teacher1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(teacher2).Error; err != nil {
		t.Fatal(err)
	}
	class1 := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	class2 := &model.Class{Name: "二班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(class2).Error; err != nil {
		t.Fatal(err)
	}
	course1 := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	course2 := &model.Course{Name: "语文", Code: "CHN", Duration: 1}
	if err := db.Create(course1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(course2).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	resp, err := svc.Generate(ctx, &dto.GenerateScheduleRequest{
		Semester:      "2024-2025-1",
		Weeks:         1,
		DaysPerWeek:   1,
		PeriodsPerDay: 1,
		Courses: []dto.CourseRequirement{
			{CourseID: course1.ID, WeeklyPeriods: 1, ClassID: class1.ID, TeacherID: teacher1.ID},
			{CourseID: course2.ID, WeeklyPeriods: 1, ClassID: class2.ID, TeacherID: teacher2.ID},
		},
		TeacherIDs:   []uint{teacher1.ID, teacher2.ID},
		ClassIDs:     []uint{class1.ID, class2.ID},
		ClassroomIDs: []uint{room1.ID, room2.ID},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Semester != "2024-2025-1" {
		t.Fatalf("expected semester echoed back, got %q", resp.Semester)
	}
	if resp.Generated != 2 || len(resp.Schedules) != 2 {
		t.Fatalf("expected 2 generated schedules, got generated=%d schedules=%d", resp.Generated, len(resp.Schedules))
	}
	if len(resp.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %+v", resp.Conflicts)
	}
}

func TestScheduleServiceGenerateKeepsOtherSemesters(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	req := func(semester string, weeks int) *dto.GenerateScheduleRequest {
		return &dto.GenerateScheduleRequest{
			Semester:      semester,
			Weeks:         weeks,
			DaysPerWeek:   1,
			PeriodsPerDay: 1,
			Courses: []dto.CourseRequirement{
				{CourseID: course.ID, WeeklyPeriods: 1, ClassID: class.ID, TeacherID: teacher.ID},
			},
			TeacherIDs:   []uint{teacher.ID},
			ClassIDs:     []uint{class.ID},
			ClassroomIDs: []uint{room.ID},
		}
	}

	if _, err := svc.Generate(ctx, req("2023-2024-2", 2)); err != nil {
		t.Fatalf("generate old semester: %v", err)
	}
	// Regenerating the new semester replaces only that semester's stale weeks.
	if _, err := svc.Generate(ctx, req("2024-2025-1", 2)); err != nil {
		t.Fatalf("generate new semester (2 weeks): %v", err)
	}
	if _, err := svc.Generate(ctx, req("2024-2025-1", 1)); err != nil {
		t.Fatalf("regenerate new semester (1 week): %v", err)
	}

	newSemester := "2024-2025-1"
	newItems, err := svc.List(ctx, &newSemester, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list new semester schedules: %v", err)
	}
	if len(newItems) != 1 {
		t.Fatalf("expected 1 schedule after regenerating semester, got %d", len(newItems))
	}
	if newItems[0].Week != 1 || newItems[0].Semester != newSemester {
		t.Fatalf("unexpected remaining schedule: %+v", newItems[0])
	}

	oldSemester := "2023-2024-2"
	oldItems, err := svc.List(ctx, &oldSemester, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list old semester schedules: %v", err)
	}
	if len(oldItems) != 2 {
		t.Fatalf("expected 2 old semester schedules to remain, got %d", len(oldItems))
	}

	// With no semester argument the latest generated semester is the default.
	defaultItems, err := svc.List(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list default semester schedules: %v", err)
	}
	if len(defaultItems) != 1 || defaultItems[0].Semester != newSemester {
		t.Fatalf("expected default list to show latest semester %s, got %+v", newSemester, defaultItems)
	}
}

func TestScheduleServiceGenerateRejectsEmptySemester(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	validReq := &dto.GenerateScheduleRequest{
		Semester:      "2024-2025-1",
		Weeks:         1,
		DaysPerWeek:   1,
		PeriodsPerDay: 1,
		Courses: []dto.CourseRequirement{
			{CourseID: course.ID, WeeklyPeriods: 1, ClassID: class.ID, TeacherID: teacher.ID},
		},
		TeacherIDs:   []uint{teacher.ID},
		ClassIDs:     []uint{class.ID},
		ClassroomIDs: []uint{room.ID},
	}
	if _, err := svc.Generate(ctx, validReq); err != nil {
		t.Fatalf("generate baseline schedule: %v", err)
	}

	emptyReq := *validReq
	emptyReq.Semester = "   "
	_, err := svc.Generate(ctx, &emptyReq)
	if !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty semester, got %v", err)
	}

	semester := "2024-2025-1"
	items, err := svc.List(ctx, &semester, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list schedules after rejected generation: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected original schedule unchanged, got %d entries", len(items))
	}
}

func TestScheduleServiceConflictsAreScopedToSemester(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room1 := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	room2 := &model.Classroom{Code: "R302", Name: "302教室", Capacity: 50}
	if err := db.Create(room1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(room2).Error; err != nil {
		t.Fatal(err)
	}
	teacher1 := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	teacher2 := &model.Teacher{Name: "李老师", EmployeeNo: "T002", Subjects: []string{"语文"}}
	if err := db.Create(teacher1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(teacher2).Error; err != nil {
		t.Fatal(err)
	}
	class1 := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	class2 := &model.Class{Name: "二班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(class2).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	// Same week/day/slot across two semesters must not be treated as a clash.
	schedules := []model.Schedule{
		{Semester: "2024-2025-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot.ID, ClassroomID: room1.ID, TeacherID: teacher1.ID, ClassID: class1.ID, CourseID: course.ID},
		{Semester: "2025-2026-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot.ID, ClassroomID: room2.ID, TeacherID: teacher2.ID, ClassID: class2.ID, CourseID: course.ID},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	latest := "2025-2026-1"
	conflicts, err := svc.CheckConflicts(ctx, &latest)
	if err != nil {
		t.Fatalf("check conflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts within a single semester, got %+v", conflicts)
	}
}

func TestScheduleServiceSwapRejectsCrossSemester(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot1 := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	slot2 := &model.TimeSlot{Code: "2", Name: "第二节", StartTime: "10:00", EndTime: "11:40"}
	if err := db.Create(slot1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(slot2).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	a := &model.Schedule{Semester: "2024-2025-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID, ClassroomID: room.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID}
	b := &model.Schedule{Semester: "2025-2026-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot2.ID, ClassroomID: room.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID}
	if err := db.Create(a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(b).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	_, err := svc.Swap(ctx, &dto.SwapScheduleRequest{ScheduleAID: a.ID, ScheduleBID: b.ID})
	if !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for cross-semester swap, got %v", err)
	}

	var storedA, storedB model.Schedule
	if err := db.First(&storedA, a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedB, b.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedA.TimeSlotID != slot1.ID || storedB.TimeSlotID != slot2.ID {
		t.Fatalf("expected both schedules unchanged, got slots %d and %d", storedA.TimeSlotID, storedB.TimeSlotID)
	}
}

func TestScheduleServiceMoveRejectsCrossSemester(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot1 := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	slot2 := &model.TimeSlot{Code: "2", Name: "第二节", StartTime: "10:00", EndTime: "11:40"}
	if err := db.Create(slot1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(slot2).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	existing := &model.Schedule{
		Semester: "2024-2025-1", Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID,
		ClassroomID: room.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID,
	}
	if err := db.Create(existing).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	otherSemester := "2025-2026-1"
	_, err := svc.Move(ctx, &dto.MoveScheduleRequest{
		ScheduleID: existing.ID, Semester: otherSemester,
		Week: 1, DayOfWeek: 1, TimeSlotID: slot2.ID, ClassroomID: room.ID,
	})
	if !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for cross-semester move, got %v", err)
	}

	var stored model.Schedule
	if err := db.First(&stored, existing.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if stored.Semester != "2024-2025-1" || stored.TimeSlotID != slot1.ID {
		t.Fatalf("expected original schedule unchanged, got semester=%q slot=%d", stored.Semester, stored.TimeSlotID)
	}

	// Moving within the same semester still works.
	_, err = svc.Move(ctx, &dto.MoveScheduleRequest{
		ScheduleID: existing.ID, Semester: "2024-2025-1",
		Week: 1, DayOfWeek: 1, TimeSlotID: slot2.ID, ClassroomID: room.ID,
	})
	if err != nil {
		t.Fatalf("move within semester: %v", err)
	}

	semester := "2024-2025-1"
	logs, total, err := svc.ListAdjustments(ctx, &semester, 1, 20)
	if err != nil {
		t.Fatalf("list adjustments: %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Semester != semester {
		t.Fatalf("expected one adjustment log in original semester, got total=%d logs=%+v", total, logs)
	}
}
