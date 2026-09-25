package service

import (
	"context"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
)

// detectConflicts inspects timetable entries (expected to belong to a single
// semester) and reports teacher/class/classroom clashes, capacity overruns and
// teacher preference violations.
func (s *scheduleService) detectConflicts(ctx context.Context, items []model.Schedule) []dto.ConflictResponse {
	conflicts := make([]dto.ConflictResponse, 0)
	teacherSlots := map[string]model.Schedule{}
	classSlots := map[string]model.Schedule{}
	classroomSlots := map[string]model.Schedule{}

	classes, _ := s.classes.GetByIDs(ctx, uniqueClassIDs(items))
	classroomList, _ := s.classrooms.GetByIDs(ctx, uniqueClassroomIDs(items))
	teacherList, _ := s.teachers.GetByIDs(ctx, uniqueTeacherIDs(items))
	slotList, _, _ := s.timeSlots.List(ctx, 1, constants.MaxPageSize)

	classMap := map[uint]model.Class{}
	for i := range classes {
		classMap[classes[i].ID] = classes[i]
	}
	classroomMap := map[uint]model.Classroom{}
	for i := range classroomList {
		classroomMap[classroomList[i].ID] = classroomList[i]
	}
	teacherMap := map[uint]model.Teacher{}
	for i := range teacherList {
		teacherMap[teacherList[i].ID] = teacherList[i]
	}
	slotMap := map[uint]model.TimeSlot{}
	for i := range slotList {
		slotMap[slotList[i].ID] = slotList[i]
	}

	for _, item := range items {
		slotKey := fmt.Sprintf("%d-%d-%d", item.Week, item.DayOfWeek, item.TimeSlotID)
		if existing, ok := teacherSlots[slotKey+"-t-"+fmt.Sprint(item.TeacherID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictTeacherTime, Semester: item.Semester, EntityType: "teacher", EntityID: item.TeacherID,
				EntityName: teacherMap[item.TeacherID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("teacher already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if existing, ok := classSlots[slotKey+"-c-"+fmt.Sprint(item.ClassID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictClassTime, Semester: item.Semester, EntityType: "class", EntityID: item.ClassID,
				EntityName: classMap[item.ClassID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("class already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if existing, ok := classroomSlots[slotKey+"-r-"+fmt.Sprint(item.ClassroomID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictClassroomTime, Semester: item.Semester, EntityType: "classroom", EntityID: item.ClassroomID,
				EntityName: classroomMap[item.ClassroomID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("classroom already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if class, ok := classMap[item.ClassID]; ok {
			if classroom, ok2 := classroomMap[item.ClassroomID]; ok2 && class.StudentCount > classroom.Capacity {
				conflicts = append(conflicts, dto.ConflictResponse{
					Type: constants.ConflictClassroomCap, Semester: item.Semester, EntityType: "class", EntityID: item.ClassID,
					EntityName: class.Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
					Suggestion: fmt.Sprintf("class size %d exceeds classroom capacity %d; choose a larger classroom", class.StudentCount, classroom.Capacity),
				})
			}
		}
		if teacher, ok := teacherMap[item.TeacherID]; ok {
			if slot, ok2 := slotMap[item.TimeSlotID]; ok2 && contains(teacher.UnavailableSlots, slot.Code) {
				conflicts = append(conflicts, dto.ConflictResponse{
					Type: constants.ConflictTeacherPref, Semester: item.Semester, EntityType: "teacher", EntityID: item.TeacherID,
					EntityName: teacher.Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
					Suggestion: fmt.Sprintf("slot %s is in the teacher's unavailable periods; choose another time", slot.Code),
				})
			}
		}
		teacherSlots[slotKey+"-t-"+fmt.Sprint(item.TeacherID)] = item
		classSlots[slotKey+"-c-"+fmt.Sprint(item.ClassID)] = item
		classroomSlots[slotKey+"-r-"+fmt.Sprint(item.ClassroomID)] = item
	}
	return conflicts
}
