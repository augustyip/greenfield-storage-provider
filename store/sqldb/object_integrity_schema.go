package sqldb

// PieceHashTable table schema.
// TODO: add timestamp.
type PieceHashTable struct {
	ObjectID       uint64 `gorm:"primary_key;index:object_id_index"`
	ReplicateIndex uint32 `gorm:"primary_key"`
	PieceIndex     uint32 `gorm:"primary_key"`
	PieceChecksum  string
}

// TableName is used to set PieceHashTable schema's table name in database
func (PieceHashTable) TableName() string {
	return PieceHashTableName
}

// IntegrityMetaTable table schema.
// TODO: add timestamp and replicateIndex.
type IntegrityMetaTable struct {
	ObjectID          uint64 `gorm:"primary_key"`
	IntegrityChecksum string
	PieceChecksumList string
	Signature         string
}

// TableName is used to set IntegrityMetaTable schema's table name in database
func (IntegrityMetaTable) TableName() string {
	return IntegrityMetaTableName
}
