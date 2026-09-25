-- Add semester isolation to timetable schedules and adjustment history.
ALTER TABLE schedules ADD COLUMN semester TEXT NOT NULL DEFAULT '未命名学期';
UPDATE schedules SET semester = '未命名学期' WHERE semester = '' OR semester IS NULL;
CREATE INDEX IF NOT EXISTS idx_schedules_semester ON schedules(semester);

ALTER TABLE adjustment_logs ADD COLUMN semester TEXT NOT NULL DEFAULT '未命名学期';
UPDATE adjustment_logs SET semester = '未命名学期' WHERE semester = '' OR semester IS NULL;
CREATE INDEX IF NOT EXISTS idx_adjustment_logs_semester ON adjustment_logs(semester);
