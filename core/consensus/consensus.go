package consensus

import (
	"context"

	paymenttypes "github.com/bnb-chain/greenfield/x/payment/types"
	sptypes "github.com/bnb-chain/greenfield/x/sp/types"
	storagetypes "github.com/bnb-chain/greenfield/x/storage/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Consensus is the interface to query greenfield consensus data. the consensus
// data can come from validator, full-node, or other off-chain data service
type Consensus interface {
	// CurrentHeight returns the current greenfield height - 1,
	CurrentHeight(ctx context.Context) (uint64, error)
	// HasAccount returns an indicator whether the account has been created.
	HasAccount(ctx context.Context, account string) (bool, error)
	// ListSPs returns all SP info.
	ListSPs(ctx context.Context) ([]*sptypes.StorageProvider, error)
	// ListBondedValidators returns all bonded validators info.
	ListBondedValidators(ctx context.Context) ([]stakingtypes.Validator, error)
	// QueryStorageParams returns the storage params.
	QueryStorageParams(ctx context.Context) (params *storagetypes.Params, err error)
	// QueryStorageParamsByTimestamp returns the storage params by block create time.
	QueryStorageParamsByTimestamp(ctx context.Context, timestamp int64) (params *storagetypes.Params, err error)
	// QueryBucketInfo returns the bucket info by bucket name.
	QueryBucketInfo(ctx context.Context, bucket string) (*storagetypes.BucketInfo, error)
	// QueryObjectInfo returns the object info by bucket and object name.
	QueryObjectInfo(ctx context.Context, bucket, object string) (*storagetypes.ObjectInfo, error)
	// QueryObjectInfoByID returns the object info by object ID.
	QueryObjectInfoByID(ctx context.Context, objectID string) (*storagetypes.ObjectInfo, error)
	// QueryBucketInfoAndObjectInfo returns the bucket and object info by bucket and object name.
	QueryBucketInfoAndObjectInfo(ctx context.Context, bucket, object string) (*storagetypes.BucketInfo, *storagetypes.ObjectInfo, error)
	// QueryPaymentStreamRecord returns the account payment status.
	QueryPaymentStreamRecord(ctx context.Context, account string) (*paymenttypes.StreamRecord, error)
	// VerifyGetObjectPermission returns an indicator whether the account has permission to get object.
	VerifyGetObjectPermission(ctx context.Context, account, bucket, object string) (bool, error)
	// VerifyPutObjectPermission returns an indicator whether the account has permission to put object.
	VerifyPutObjectPermission(ctx context.Context, account, bucket, object string) (bool, error)
	// ListenObjectSeal returns an indicator whether the object is successfully sealed before timeOutHeight.
	ListenObjectSeal(ctx context.Context, objectID uint64, timeOutHeight int) (bool, error)
	// ListenRejectUnSealObject returns an indication of the object is rejected.
	ListenRejectUnSealObject(ctx context.Context, objectID uint64, timeoutHeight int) (bool, error)
	// Close the Consensus interface.
	Close() error
}

var _ Consensus = (*NullConsensus)(nil)

type NullConsensus struct{}

func (*NullConsensus) CurrentHeight(context.Context) (uint64, error) { return 0, nil }
func (*NullConsensus) HasAccount(context.Context, string) (bool, error) {
	return false, nil
}
func (*NullConsensus) ListSPs(context.Context) ([]*sptypes.StorageProvider, error) {
	return nil, nil
}

func (*NullConsensus) ListBondedValidators(context.Context) ([]stakingtypes.Validator, error) {
	return nil, nil
}

func (*NullConsensus) QueryStorageParams(context.Context) (*storagetypes.Params, error) {
	return nil, nil
}
func (*NullConsensus) QueryStorageParamsByTimestamp(context.Context, int64) (*storagetypes.Params, error) {
	return nil, nil
}
func (*NullConsensus) QueryBucketInfo(context.Context, string) (*storagetypes.BucketInfo, error) {
	return nil, nil
}
func (*NullConsensus) QueryObjectInfo(context.Context, string, string) (*storagetypes.ObjectInfo, error) {
	return nil, nil
}
func (*NullConsensus) QueryObjectInfoByID(context.Context, string) (*storagetypes.ObjectInfo, error) {
	return nil, nil
}
func (*NullConsensus) QueryBucketInfoAndObjectInfo(context.Context, string, string) (*storagetypes.BucketInfo, *storagetypes.ObjectInfo, error) {
	return nil, nil, nil
}
func (*NullConsensus) QueryPaymentStreamRecord(context.Context, string) (*paymenttypes.StreamRecord, error) {
	return nil, nil
}
func (*NullConsensus) VerifyGetObjectPermission(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (*NullConsensus) VerifyPutObjectPermission(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (*NullConsensus) ListenObjectSeal(context.Context, uint64, int) (bool, error) {
	return false, nil
}
func (*NullConsensus) ListenRejectUnSealObject(context.Context, uint64, int) (bool, error) {
	return false, nil
}
func (*NullConsensus) Close() error { return nil }
