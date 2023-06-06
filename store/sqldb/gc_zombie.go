package sqldb

import (
	"fmt"

	"github.com/bnb-chain/greenfield-storage-provider/core/spdb"
	"gorm.io/gorm"
)

// InsertGCZombieProgress is used to insert gc zombie progress.
func (s *SpDBImpl) InsertGCZombieProgress(taskKey string, gcMeta *spdb.GCZombieMeta) error {
	if result := s.db.Create(&GCZombieProgressTable{
		TaskKey:               taskKey,
		StartGCObjectID:       gcMeta.StartObjectID,
		LastDeletedObjectID:   0,
		DeletedZombieNumber:   0,
		CreateTimestampSecond: GetCurrentUnixTime(),
		UpdateTimestampSecond: GetCurrentUnixTime(),
	}); result.Error != nil || result.RowsAffected != 1 {
		return fmt.Errorf("failed to insert gc zombie record: %s", result.Error)
	}
	return nil
}

// DeleteGCZombieProgress is used to delete gc zombie task.
func (s *SpDBImpl) DeleteGCZombieProgress(taskKey string) error {
	return s.db.Delete(&GCZombieProgressTable{
		TaskKey: taskKey, // should be the primary key
	}).Error
}

func (s *SpDBImpl) UpdateGCZombieProgress(gcMeta *spdb.GCZombieMeta) error {
	if result := s.db.Model(&GCZombieProgressTable{}).Where("task_key = ?", gcMeta.TaskKey).Updates(&GCZombieProgressTable{
		LastDeletedObjectID:   gcMeta.LastDeletedObjectID,
		DeletedZombieNumber:   gcMeta.GCZombieNumber,
		UpdateTimestampSecond: GetCurrentUnixTime(),
	}); result.Error != nil {
		return fmt.Errorf("failed to update gc zombie record: %s", result.Error)
	}
	return nil
}

func (s *SpDBImpl) GetGCMetasToGCZombie(limit int) ([]*spdb.GCZombieMeta, error) {
	var (
		result        *gorm.DB
		gcProgresses  []GCZombieProgressTable
		returnGCMetas []*spdb.GCZombieMeta
	)
	result = s.db.Order("update_timestamp_second DESC").Limit(limit).Find(&gcProgresses)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to query gc object table: %s", result.Error)
	}
	for _, g := range gcProgresses {
		returnGCMetas = append(returnGCMetas, &spdb.GCZombieMeta{
			TaskKey:             g.TaskKey,
			StartObjectID:       g.StartGCObjectID,
			LastDeletedObjectID: g.LastDeletedObjectID,
			GCZombieNumber:      g.DeletedZombieNumber,
		})
	}
	return returnGCMetas, nil
}
