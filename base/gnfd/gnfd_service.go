package gnfd

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bnb-chain/greenfield-storage-provider/pkg/metrics"
	virtualgrouptypes "github.com/bnb-chain/greenfield/x/virtualgroup/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	paymenttypes "github.com/bnb-chain/greenfield/x/payment/types"
	permissiontypes "github.com/bnb-chain/greenfield/x/permission/types"
	sptypes "github.com/bnb-chain/greenfield/x/sp/types"
	storagetypes "github.com/bnb-chain/greenfield/x/storage/types"
)

// CurrentHeight the block height sub one as the stable height.
func (g *Gnfd) CurrentHeight(ctx context.Context) (uint64, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_height").Observe(time.Since(startTime).Seconds())
	resp, err := g.getCurrentWsClient().ABCIInfo(ctx)
	if err != nil {
		log.CtxErrorw(ctx, "get latest block height failed", "node_addr",
			g.getCurrentWsClient().Remote(), "error", err)
		return 0, err
	}
	return (uint64)(resp.Response.LastBlockHeight), nil
}

// HasAccount returns an indication of the existence of address.
func (g *Gnfd) HasAccount(ctx context.Context, address string) (bool, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_account").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.Account(ctx, &authtypes.QueryAccountRequest{Address: address})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query account", "address", address, "error", err)
		return false, err
	}
	return resp.GetAccount() != nil, nil
}

// ListSPs returns the list of storage provider info.
func (g *Gnfd) ListSPs(ctx context.Context) ([]*sptypes.StorageProvider, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("list_sps").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	var spInfos []*sptypes.StorageProvider
	resp, err := client.StorageProviders(ctx, &sptypes.QueryStorageProvidersRequest{
		Pagination: &query.PageRequest{
			Offset: 0,
			Limit:  math.MaxUint64,
		},
	})
	if err != nil {
		log.Errorw("failed to list storage providers", "error", err)
		return spInfos, err
	}
	for i := 0; i < len(resp.GetSps()); i++ {
		spInfos = append(spInfos, resp.GetSps()[i])
	}
	return spInfos, nil
}

// QuerySP returns the sp info.
func (g *Gnfd) QuerySP(ctx context.Context, operatorAddress string) (*sptypes.StorageProvider, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_sp").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.StorageProviderByOperatorAddress(ctx, &sptypes.QueryStorageProviderByOperatorAddressRequest{
		OperatorAddress: operatorAddress,
	})
	if err != nil {
		log.Errorw("failed to query storage provider", "error", err)
		return nil, err
	}
	return resp.GetStorageProvider(), nil
}

// ListBondedValidators returns the list of bonded validators.
func (g *Gnfd) ListBondedValidators(ctx context.Context) ([]stakingtypes.Validator, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("list_bonded_validators").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	var validators []stakingtypes.Validator
	resp, err := client.Validators(ctx, &stakingtypes.QueryValidatorsRequest{Status: "BOND_STATUS_BONDED"})
	if err != nil {
		log.Errorw("failed to list validators", "error", err)
		return validators, err
	}
	for i := 0; i < len(resp.GetValidators()); i++ {
		validators = append(validators, resp.GetValidators()[i])
	}
	return validators, nil
}

// ListVirtualGroupFamilies return the list of virtual group family.
func (g *Gnfd) ListVirtualGroupFamilies(ctx context.Context, spID uint32) ([]*virtualgrouptypes.GlobalVirtualGroupFamily, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("list_virtual_group_family").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	var vgfs []*virtualgrouptypes.GlobalVirtualGroupFamily
	resp, err := client.VirtualGroupQueryClient.GlobalVirtualGroupFamilies(ctx, &virtualgrouptypes.QueryGlobalVirtualGroupFamiliesRequest{
		StorageProviderId: spID,
	})
	if err != nil {
		log.Errorw("failed to list virtual group families", "error", err)
		return vgfs, err
	}
	for i := 0; i < len(resp.GetGlobalVirtualGroupFamilies()); i++ {
		vgfs = append(vgfs, resp.GetGlobalVirtualGroupFamilies()[i])
	}
	return vgfs, nil
}

// QueryVirtualGroupFamily returns the virtual group family.
func (g *Gnfd) QueryVirtualGroupFamily(ctx context.Context, spID, vgfID uint32) (*virtualgrouptypes.GlobalVirtualGroupFamily, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_virtual_group_family").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.VirtualGroupQueryClient.GlobalVirtualGroupFamily(ctx, &virtualgrouptypes.QueryGlobalVirtualGroupFamilyRequest{
		StorageProviderId: spID,
		FamilyId:          vgfID,
	})
	if err != nil {
		log.Errorw("failed to query virtual group family", "error", err)
		return nil, err
	}
	return resp.GetGlobalVirtualGroupFamily(), nil
}

// QueryGlobalVirtualGroup returns the global virtual group info.
func (g *Gnfd) QueryGlobalVirtualGroup(ctx context.Context, gvgID uint32) (*virtualgrouptypes.GlobalVirtualGroup, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_global_virtual_group").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.VirtualGroupQueryClient.GlobalVirtualGroup(ctx, &virtualgrouptypes.QueryGlobalVirtualGroupRequest{
		GlobalVirtualGroupId: gvgID,
	})
	if err != nil {
		log.Errorw("failed to query global virtual group", "error", err)
		return nil, err
	}
	return resp.GetGlobalVirtualGroup(), nil
}

// QueryVirtualGroupParams return virtual group params.
func (g *Gnfd) QueryVirtualGroupParams(ctx context.Context) (*virtualgrouptypes.Params, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_virtual_group_params").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.VirtualGroupQueryClient.Params(ctx, &virtualgrouptypes.QueryParamsRequest{})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query virtual group params", "error", err)
		return nil, err
	}
	return &resp.Params, nil
}

// QueryStorageParams returns storage params
func (g *Gnfd) QueryStorageParams(ctx context.Context) (params *storagetypes.Params, err error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_storage_params").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.StorageQueryClient.Params(ctx, &storagetypes.QueryParamsRequest{})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query storage params", "error", err)
		return nil, err
	}
	return &resp.Params, nil
}

// QueryStorageParamsByTimestamp returns storage params by block create time.
func (g *Gnfd) QueryStorageParamsByTimestamp(ctx context.Context, timestamp int64) (params *storagetypes.Params, err error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_storage_params_by_timestamp").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.StorageQueryClient.QueryParamsByTimestamp(ctx,
		&storagetypes.QueryParamsByTimestampRequest{Timestamp: timestamp})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query storage params", "error", err)
		return nil, err
	}
	return &resp.Params, nil
}

// QueryBucketInfo returns the bucket info by name.
func (g *Gnfd) QueryBucketInfo(ctx context.Context, bucket string) (*storagetypes.BucketInfo, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_bucket").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.HeadBucket(ctx, &storagetypes.QueryHeadBucketRequest{BucketName: bucket})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query bucket", "bucket_name", bucket, "error", err)
		return nil, err
	}
	return resp.GetBucketInfo(), nil
}

// QueryObjectInfo returns the object info by name.
func (g *Gnfd) QueryObjectInfo(ctx context.Context, bucket, object string) (*storagetypes.ObjectInfo, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_object").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.HeadObject(ctx, &storagetypes.QueryHeadObjectRequest{
		BucketName: bucket,
		ObjectName: object,
	})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query object", "bucket_name", bucket, "object_name", object, "error", err)
		return nil, err
	}
	return resp.GetObjectInfo(), nil
}

// QueryObjectInfoByID returns the object info by name.
func (g *Gnfd) QueryObjectInfoByID(ctx context.Context, objectID string) (*storagetypes.ObjectInfo, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_object_by_id").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.HeadObjectById(ctx, &storagetypes.QueryHeadObjectByIdRequest{
		ObjectId: objectID,
	})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query object", "object_id", objectID, "error", err)
		return nil, err
	}
	return resp.GetObjectInfo(), nil
}

// QueryBucketInfoAndObjectInfo returns bucket info and object info, if not found, return the corresponding error code
func (g *Gnfd) QueryBucketInfoAndObjectInfo(ctx context.Context, bucket, object string) (*storagetypes.BucketInfo,
	*storagetypes.ObjectInfo, error) {
	bucketInfo, err := g.QueryBucketInfo(ctx, bucket)
	if err != nil {
		return nil, nil, err
	}
	objectInfo, err := g.QueryObjectInfo(ctx, bucket, object)
	if err != nil {
		return bucketInfo, nil, err
	}
	return bucketInfo, objectInfo, nil
}

// ListenObjectSeal returns an indication of the object is sealed.
// TODO:: retrieve service support seal event subscription
func (g *Gnfd) ListenObjectSeal(ctx context.Context, objectID uint64, timeoutHeight int) (bool, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("wait_object_seal").Observe(time.Since(startTime).Seconds())
	var (
		objectInfo *storagetypes.ObjectInfo
		err        error
	)
	for i := 0; i < timeoutHeight; i++ {
		objectInfo, err = g.QueryObjectInfoByID(ctx, strconv.FormatUint(objectID, 10))
		if err != nil {
			time.Sleep(ExpectedOutputBlockInternal * time.Second)
			continue
		}
		if objectInfo.GetObjectStatus() == storagetypes.OBJECT_STATUS_SEALED {
			log.CtxDebugw(ctx, "succeed to listen object stat")
			return true, nil
		}
		time.Sleep(ExpectedOutputBlockInternal * time.Second)
	}
	if err == nil {
		log.CtxErrorw(ctx, "seal object timeout", "object_id", objectID)
		return false, ErrSealTimeout
	}
	log.CtxErrorw(ctx, "failed to listen seal object", "object_id", objectID, "error", err)
	return false, err
}

// ListenRejectUnSealObject returns an indication of the object is rejected.
// TODO:: retrieve service support reject unseal event subscription
func (g *Gnfd) ListenRejectUnSealObject(ctx context.Context, objectID uint64, timeoutHeight int) (bool, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("wait_reject_unseal_object").Observe(time.Since(startTime).Seconds())
	var err error
	for i := 0; i < timeoutHeight; i++ {
		_, err = g.QueryObjectInfoByID(ctx, strconv.FormatUint(objectID, 10))
		if err != nil {
			if strings.Contains(err.Error(), "No such object") {
				return true, nil
			}
		}
		time.Sleep(ExpectedOutputBlockInternal * time.Second)
	}
	if err == nil {
		log.CtxErrorw(ctx, "reject unseal object timeout", "object_id", objectID)
		return false, ErrRejectUnSealTimeout
	}
	log.CtxErrorw(ctx, "failed to listen reject unseal object", "object_id", objectID, "error", err)
	return false, err
}

// QueryPaymentStreamRecord returns the steam record info by account.
func (g *Gnfd) QueryPaymentStreamRecord(ctx context.Context, account string) (*paymenttypes.StreamRecord, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("query_payment_stream_record").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.StreamRecord(ctx, &paymenttypes.QueryGetStreamRecordRequest{
		Account: account,
	})
	if err != nil {
		log.CtxErrorw(ctx, "failed to query stream record", "account", account, "error", err)
		return nil, err
	}
	return &resp.StreamRecord, nil
}

// VerifyGetObjectPermission verifies get object permission.
func (g *Gnfd) VerifyGetObjectPermission(ctx context.Context, account, bucket, object string) (bool, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("verify_get_object_permission").Observe(time.Since(startTime).Seconds())
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.VerifyPermission(ctx, &storagetypes.QueryVerifyPermissionRequest{
		Operator:   account,
		BucketName: bucket,
		ObjectName: object,
		ActionType: permissiontypes.ACTION_GET_OBJECT,
	})
	if err != nil {
		log.CtxErrorw(ctx, "failed to verify get object permission", "account", account, "error", err)
		return false, err
	}
	if resp.GetEffect() == permissiontypes.EFFECT_ALLOW {
		return true, err
	}
	return false, err
}

// VerifyPutObjectPermission verifies put object permission.
func (g *Gnfd) VerifyPutObjectPermission(ctx context.Context, account, bucket, object string) (bool, error) {
	startTime := time.Now()
	defer metrics.GnfdChainHistogram.WithLabelValues("verify_put_object_permission").Observe(time.Since(startTime).Seconds())
	_ = object
	client := g.getCurrentClient().GnfdClient()
	resp, err := client.VerifyPermission(ctx, &storagetypes.QueryVerifyPermissionRequest{
		Operator:   account,
		BucketName: bucket,
		// TODO: Polish the function interface according to the semantics
		// ObjectName: object,
		ActionType: permissiontypes.ACTION_CREATE_OBJECT,
	})
	if err != nil {
		log.CtxErrorw(ctx, "failed to verify put object permission", "account", account, "error", err)
		return false, err
	}
	if resp.GetEffect() == permissiontypes.EFFECT_ALLOW {
		return true, err
	}
	return false, err
}
