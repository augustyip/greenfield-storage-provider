package sqldb

// GCZombieProgressTable table schema
type GCZombieProgressTable struct {
	TaskKey               string `gorm:"primary_key"`
	StartGCObjectID       uint64
	EndGCObjectID         uint64 // is unused, reserved to support multi-task.
	LastDeletedObjectID   uint64
	DeletedZombieNumber   uint64
	CreateTimestampSecond int64
	UpdateTimestampSecond int64 `gorm:"index:update_timestamp_index"`
}

// TableName is used to set GCZombieProgressTable Schema's table name in database
func (GCZombieProgressTable) TableName() string {
	return GCZombieProgressTableName
}
