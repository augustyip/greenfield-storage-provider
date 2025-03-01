package executor

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bnb-chain/greenfield-common/go/hash"
	"github.com/bnb-chain/greenfield-common/go/redundancy"
	"github.com/bnb-chain/greenfield-storage-provider/base/types/gfsperrors"
	"github.com/bnb-chain/greenfield-storage-provider/core/module"
	"github.com/bnb-chain/greenfield-storage-provider/core/spdb"
	coretask "github.com/bnb-chain/greenfield-storage-provider/core/task"
	"github.com/bnb-chain/greenfield-storage-provider/modular/manager"
	"github.com/bnb-chain/greenfield-storage-provider/modular/metadata/types"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/metrics"
	storagetypes "github.com/bnb-chain/greenfield/x/storage/types"
)

var (
	ErrDanglingPointer         = gfsperrors.Register(module.ExecuteModularName, http.StatusBadRequest, 40001, "OoooH.... request lost")
	ErrInsufficientApproval    = gfsperrors.Register(module.ExecuteModularName, http.StatusNotFound, 40002, "insufficient approvals from p2p")
	ErrUnsealed                = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 40003, "seal object on chain failed")
	ErrExhaustedApproval       = gfsperrors.Register(module.ExecuteModularName, http.StatusNotFound, 40004, "approvals exhausted")
	ErrInvalidIntegrity        = gfsperrors.Register(module.ExecuteModularName, http.StatusNotAcceptable, 40005, "secondary integrity hash verification failed")
	ErrSecondaryMismatch       = gfsperrors.Register(module.ExecuteModularName, http.StatusNotAcceptable, 40006, "secondary sp mismatch")
	ErrReplicateIdsOutOfBounds = gfsperrors.Register(module.ExecuteModularName, http.StatusNotAcceptable, 40007, "replicate idx out of bounds")
	ErrGfSpDB                  = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45201, "server slipped away, try again later")
	ErrRecoveryRedundancyType  = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45202, "recovery only support EC redundancy type")
	ErrRecoveryPieceNotEnough  = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45203, "fail to get enough piece data to recovery")
	ErrRecoveryDecode          = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45204, "EC decode error")
	ErrPieceStore              = gfsperrors.Register(module.ReceiveModularName, http.StatusInternalServerError, 45205, "server slipped away, try again later")
	ErrRecoveryPieceChecksum   = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45206, "recovery checksum not correct")
	ErrRecoveryPieceLength     = gfsperrors.Register(module.ExecuteModularName, http.StatusInternalServerError, 45207, "get secondary piece data length error")
	ErrPrimaryNotFound         = gfsperrors.Register(module.ExecuteModularName, http.StatusNotAcceptable, 45208, "primary sp endpoint not found when recoverying")
	ErrRecoveryPieceIndex      = gfsperrors.Register(module.ExecuteModularName, http.StatusNotAcceptable, 45209, "recovery piece index invalid")
)

func (e *ExecuteModular) HandleSealObjectTask(ctx context.Context, task coretask.SealObjectTask) {
	if task == nil || task.GetObjectInfo() == nil {
		log.CtxErrorw(ctx, "failed to handle seal object, task pointer dangling")
		task.SetError(ErrDanglingPointer)
		return
	}
	sealMsg := &storagetypes.MsgSealObject{
		Operator:              e.baseApp.OperatorAddress(),
		BucketName:            task.GetObjectInfo().GetBucketName(),
		ObjectName:            task.GetObjectInfo().GetObjectName(),
		SecondarySpAddresses:  task.GetSecondaryAddresses(),
		SecondarySpSignatures: task.GetSecondarySignatures(),
	}
	task.SetError(e.sealObject(ctx, task, sealMsg))
	log.CtxDebugw(ctx, "finish to handle seal object task", "error", task.Error())
}

func (e *ExecuteModular) sealObject(ctx context.Context, task coretask.ObjectTask, sealMsg *storagetypes.MsgSealObject) error {
	var err error
	for retry := int64(0); retry <= task.GetMaxRetry(); retry++ {
		e.baseApp.GfSpDB().InsertUploadEvent(task.GetObjectInfo().Id.Uint64(), spdb.ExecutorBeginSealTx, task.Key().String())
		err = e.baseApp.GfSpClient().SealObject(ctx, sealMsg)
		if err != nil {
			log.CtxErrorw(ctx, "failed to seal object", "retry", retry,
				"max_retry", task.GetMaxRetry(), "error", err)
			e.baseApp.GfSpDB().InsertUploadEvent(task.GetObjectInfo().Id.Uint64(), spdb.ExecutorEndSealTx, task.Key().String()+":"+err.Error())
			time.Sleep(time.Duration(e.listenSealRetryTimeout) * time.Second)
		} else {
			e.baseApp.GfSpDB().InsertUploadEvent(task.GetObjectInfo().Id.Uint64(), spdb.ExecutorEndSealTx, task.Key().String())
			break
		}
	}
	// even though signer return error, maybe seal on chain successfully because
	// signer use the async mode, so ignore the error and listen directly
	err = e.listenSealObject(ctx, task.GetObjectInfo())
	if err == nil {
		metrics.PerfUploadTimeHistogram.WithLabelValues("upload_replicate_seal_total_time").Observe(time.Since(time.Unix(task.GetCreateTime(), 0)).Seconds())
	}
	return err
}

func (e *ExecuteModular) listenSealObject(ctx context.Context, object *storagetypes.ObjectInfo) error {
	var err error
	for retry := 0; retry < e.maxListenSealRetry; retry++ {
		e.baseApp.GfSpDB().InsertUploadEvent(object.Id.Uint64(), spdb.ExecutorBeginConfirmSeal, "")
		sealed, innerErr := e.baseApp.Consensus().ListenObjectSeal(ctx,
			object.Id.Uint64(), e.listenSealTimeoutHeight)
		if innerErr != nil {
			e.baseApp.GfSpDB().InsertUploadEvent(object.Id.Uint64(), spdb.ExecutorEndConfirmSeal, "err:"+innerErr.Error())
			log.CtxErrorw(ctx, "failed to listen object seal", "retry", retry,
				"max_retry", e.maxListenSealRetry, "error", err)
			time.Sleep(time.Duration(e.listenSealRetryTimeout) * time.Second)
			err = innerErr
			continue
		}
		if !sealed {
			e.baseApp.GfSpDB().InsertUploadEvent(object.Id.Uint64(), spdb.ExecutorEndConfirmSeal, "unsealed")
			log.CtxErrorw(ctx, "failed to seal object on chain", "retry", retry,
				"max_retry", e.maxListenSealRetry, "error", err)
			err = ErrUnsealed
			continue
		}
		e.baseApp.GfSpDB().InsertUploadEvent(object.Id.Uint64(), spdb.ExecutorEndConfirmSeal, "sealed")
		err = nil
		break
	}
	return err
}

func (e *ExecuteModular) HandleReceivePieceTask(ctx context.Context, task coretask.ReceivePieceTask) {
	if task.GetObjectInfo() == nil {
		log.CtxErrorw(ctx, "failed to handle receive piece confirm, task pointer dangling")
		return
	}
	var (
		err           error
		onChainObject *storagetypes.ObjectInfo
	)
	err = e.listenSealObject(ctx, task.GetObjectInfo())
	if err == nil {
		task.SetSealed(true)
	}
	log.CtxDebugw(ctx, "finish to listen seal object for receive piece task, "+
		"begin to check secondary sp", "error", err)

	onChainObject, err = e.baseApp.Consensus().QueryObjectInfo(ctx, task.GetObjectInfo().GetBucketName(),
		task.GetObjectInfo().GetObjectName())
	if err != nil {
		log.CtxErrorw(ctx, "failed to get object info", "error", err)
		task.SetError(err)
		return
	}
	if onChainObject.GetObjectStatus() != storagetypes.OBJECT_STATUS_SEALED {
		log.CtxErrorw(ctx, "failed to confirm receive task, object is unsealed")
		task.SetError(ErrUnsealed)
		return
	}
	// regardless of whether the sp is as secondary or not, it needs to be set to the
	// sealed state to let the manager clear the task.
	task.SetSealed(true)
	if int(task.GetReplicateIdx()) >= len(onChainObject.GetSecondarySpAddresses()) {
		log.CtxErrorw(ctx, "failed to confirm receive task, replicate idx out of bounds",
			"replicate_idx", task.GetReplicateIdx(),
			"secondary_sp_len", len(onChainObject.GetSecondarySpAddresses()))
		task.SetError(ErrReplicateIdsOutOfBounds)
		return
	}
	if onChainObject.GetSecondarySpAddresses()[int(task.GetReplicateIdx())] != e.baseApp.OperatorAddress() {
		log.CtxErrorw(ctx, "failed to confirm receive task, secondary sp mismatch",
			"expect", onChainObject.GetSecondarySpAddresses()[int(task.GetReplicateIdx())],
			"current", e.baseApp.OperatorAddress())
		task.SetError(ErrSecondaryMismatch)
		// TODO:: gc zombie task will gc the zombie piece, it is a conservative plan
		err = e.baseApp.GfSpDB().DeleteObjectIntegrity(task.GetObjectInfo().Id.Uint64())
		if err != nil {
			log.CtxErrorw(ctx, "failed to delete integrity")
		}
		var pieceKey string
		segmentCount := e.baseApp.PieceOp().SegmentPieceCount(onChainObject.GetPayloadSize(),
			task.GetStorageParams().GetMaxPayloadSize())
		for i := uint32(0); i < segmentCount; i++ {
			if task.GetObjectInfo().GetRedundancyType() == storagetypes.REDUNDANCY_EC_TYPE {
				pieceKey = e.baseApp.PieceOp().ECPieceKey(onChainObject.Id.Uint64(),
					i, task.GetReplicateIdx())
			} else {
				pieceKey = e.baseApp.PieceOp().SegmentPieceKey(onChainObject.Id.Uint64(), i)
			}
			err = e.baseApp.PieceStore().DeletePiece(ctx, pieceKey)
			if err != nil {
				log.CtxErrorw(ctx, "failed to delete piece data", "piece_key", pieceKey)
			}
		}
		return
	}
	log.CtxDebugw(ctx, "succeed to handle confirm receive piece task")
}

func (e *ExecuteModular) HandleGCObjectTask(ctx context.Context, task coretask.GCObjectTask) {
	var (
		err                error
		waitingGCObjects   []*types.Object
		currentGCBlockID   uint64
		currentGCObjectID  uint64
		responseEndBlockID uint64
		storageParams      *storagetypes.Params
		gcObjectNumber     int
		tryAgainLater      bool
		taskIsCanceled     bool
		hasNoObject        bool
		isSucceed          bool
	)

	reportProgress := func() bool {
		reportErr := e.ReportTask(ctx, task)
		log.CtxDebugw(ctx, "gc object task report progress", "task_info", task.Info(), "error", reportErr)
		return errors.Is(reportErr, manager.ErrCanceledTask)
	}

	defer func() {
		if err == nil && (isSucceed || hasNoObject) { // succeed
			task.SetCurrentBlockNumber(task.GetEndBlockNumber() + 1)
			reportProgress()
		} else { // failed
			task.SetError(err)
			reportProgress()
		}
		log.CtxDebugw(ctx, "gc object task",
			"task_info", task.Info(), "is_succeed", isSucceed,
			"response_end_block_id", responseEndBlockID, "waiting_gc_object_number", len(waitingGCObjects),
			"has_gc_object_number", gcObjectNumber, "try_again_later", tryAgainLater,
			"task_is_canceled", taskIsCanceled, "has_no_object", hasNoObject, "error", err)
	}()

	if waitingGCObjects, responseEndBlockID, err = e.baseApp.GfSpClient().ListDeletedObjectsByBlockNumberRange(
		ctx, e.baseApp.OperatorAddress(), task.GetStartBlockNumber(),
		task.GetEndBlockNumber(), true); err != nil {
		log.CtxErrorw(ctx, "failed to query deleted object list", "task_info", task.Info(), "error", err)
		return
	}
	if responseEndBlockID < task.GetStartBlockNumber() || responseEndBlockID < task.GetEndBlockNumber() {
		tryAgainLater = true
		log.CtxInfow(ctx, "metadata is not latest, try again later",
			"response_end_block_id", responseEndBlockID, "task_info", task.Info())
		return
	}
	if len(waitingGCObjects) == 0 {
		hasNoObject = true
		return
	}

	for _, object := range waitingGCObjects {
		if storageParams, err = e.baseApp.Consensus().QueryStorageParamsByTimestamp(
			context.Background(), object.GetObjectInfo().GetCreateAt()); err != nil {
			log.Errorw("failed to query storage params", "task_info", task.Info(), "error", err)
			return
		}

		currentGCBlockID = uint64(object.GetDeleteAt())
		objectInfo := object.GetObjectInfo()
		currentGCObjectID = objectInfo.Id.Uint64()
		if currentGCBlockID < task.GetCurrentBlockNumber() {
			log.Errorw("skip gc object", "object_info", objectInfo,
				"task_current_gc_block_id", task.GetCurrentBlockNumber())
			continue
		}
		segmentCount := e.baseApp.PieceOp().SegmentPieceCount(
			objectInfo.GetPayloadSize(), storageParams.VersionedParams.GetMaxSegmentSize())
		for segIdx := uint32(0); segIdx < segmentCount; segIdx++ {
			pieceKey := e.baseApp.PieceOp().SegmentPieceKey(currentGCObjectID, segIdx)
			// ignore this delete api error, TODO: refine gc workflow by enrich metadata index.
			deleteErr := e.baseApp.PieceStore().DeletePiece(ctx, pieceKey)
			log.CtxDebugw(ctx, "delete the primary sp pieces",
				"object_info", objectInfo, "piece_key", pieceKey, "error", deleteErr)
		}
		for rIdx, address := range objectInfo.GetSecondarySpAddresses() {
			if strings.Compare(e.baseApp.OperatorAddress(), address) == 0 {
				for segIdx := uint32(0); segIdx < segmentCount; segIdx++ {
					pieceKey := e.baseApp.PieceOp().ECPieceKey(currentGCObjectID, segIdx, uint32(rIdx))
					if objectInfo.GetRedundancyType() == storagetypes.REDUNDANCY_REPLICA_TYPE {
						pieceKey = e.baseApp.PieceOp().SegmentPieceKey(objectInfo.Id.Uint64(), segIdx)
					}
					// ignore this delete api error, TODO: refine gc workflow by enrich metadata index.
					deleteErr := e.baseApp.PieceStore().DeletePiece(ctx, pieceKey)
					log.CtxDebugw(ctx, "delete the secondary sp pieces",
						"object_info", objectInfo, "piece_key", pieceKey, "error", deleteErr)
				}
			}
		}
		// ignore this delete api error, TODO: refine gc workflow by enrich metadata index.
		deleteErr := e.baseApp.GfSpDB().DeleteObjectIntegrity(objectInfo.Id.Uint64())
		log.CtxDebugw(ctx, "delete the object integrity meta", "object_info", objectInfo, "error", deleteErr)
		task.SetCurrentBlockNumber(currentGCBlockID)
		task.SetLastDeletedObjectId(currentGCObjectID)
		metrics.GCObjectCounter.WithLabelValues(e.Name()).Inc()
		if taskIsCanceled = reportProgress(); taskIsCanceled {
			log.CtxErrorw(ctx, "gc object task has been canceled", "current_gc_object_info", objectInfo, "task_info", task.Info())
			return
		}
		log.CtxDebugw(ctx, "succeed to gc an object", "object_info", objectInfo, "deleted_at_block_id", currentGCBlockID)
		gcObjectNumber++
	}
	isSucceed = true
}

func (e *ExecuteModular) HandleGCZombiePieceTask(ctx context.Context, task coretask.GCZombiePieceTask) {
	log.CtxWarn(ctx, "gc zombie piece future support")
}

func (e *ExecuteModular) HandleGCMetaTask(ctx context.Context, task coretask.GCMetaTask) {
	log.CtxWarn(ctx, "gc meta future support")
}

// HandleRecoverPieceTask handle the recovery piece task, it will send request to other SPs to get piece data to recovery,
// recovery the original data, and write the recovered data to piece store
func (e *ExecuteModular) HandleRecoverPieceTask(ctx context.Context, task coretask.RecoveryPieceTask) {
	var (
		dataShards         = task.GetStorageParams().VersionedParams.GetRedundantDataChunkNum()
		maxSegmentSize     = task.GetStorageParams().VersionedParams.GetMaxSegmentSize()
		parityShards       = task.GetStorageParams().VersionedParams.GetRedundantParityChunkNum()
		minRecoveryPieces  = dataShards
		ecPieceCount       = dataShards + parityShards
		doneTaskNum        = uint32(0)
		recoveryKey        string
		recoveryMinEcIndex = -1
		err                error
		finishRecovery     = false
	)
	defer func() {
		if err != nil {
			task.SetError(err)
		}
		if task.Error() != nil {
			log.CtxErrorw(ctx, "recovery task failed", "error", task.Error())
		}
		if finishRecovery {
			task.SetRecoverDone()
		}
	}()

	if task == nil || task.GetObjectInfo() == nil || task.GetStorageParams() == nil {
		err = ErrDanglingPointer
		return
	}

	if task.GetObjectInfo().GetRedundancyType() != storagetypes.REDUNDANCY_EC_TYPE {
		err = ErrRecoveryRedundancyType
		return
	}

	ecIndex := task.GetEcIdx()
	maxRedundancyIndex := int32(ecPieceCount) - 1

	if ecIndex < int32(recoveryMinEcIndex) || ecIndex > maxRedundancyIndex {
		err = ErrRecoveryPieceIndex
		return
	}

	// recovery secondary SP
	if ecIndex >= 0 {
		objectId := task.GetObjectInfo().Id.Uint64()
		segmentIdx := task.GetSegmentIdx()
		primarySPEndpoint, err := e.getObjectPrimarySPEndpoint(ctx, task.GetObjectInfo().BucketName)
		if err != nil {
			task.SetError(err)
			return
		}
		// TODO if get piece from primary SP fail ,send request to other secondary SP
		pieceData, err := e.doRecoveryPiece(ctx, task, primarySPEndpoint)
		if err != nil {
			task.SetError(err)
			return
		}
		// compare integrity hash
		if err = e.checkRecoveryCheckSum(ctx, task, hash.GenerateChecksum(pieceData)); err != nil {
			return
		}

		recoveryKey = e.baseApp.PieceOp().ECPieceKey(objectId, segmentIdx, uint32(ecIndex))
		// write the recovery segment key to keystore
		err = e.baseApp.PieceStore().PutPiece(ctx, recoveryKey, pieceData)
		if err != nil {
			log.CtxErrorw(ctx, "EC recover data write piece fail", "pieceKey:", recoveryKey, "error", err)
			task.SetError(err)
			return
		}
		log.CtxDebugw(ctx, "secondary SP recovery successfully", "pieceKey:", recoveryKey)
		finishRecovery = true
		return
	} else {
		// recovery primary SP
		// TODO(leo) Encapsulate the following code into a function recoveryPrimarySP()
		log.CtxDebugw(ctx, "begin to recovery primary SP object")
		secondaryEndpoints, err := e.getObjectSecondaryEndpoints(ctx, task.GetObjectInfo())
		if err != nil {
			return
		}

		var recoveryDataSources = make([][]byte, len(secondaryEndpoints))
		doneCh := make(chan bool, len(secondaryEndpoints))
		quitCh := make(chan bool)

		totalTaskNum := int32(ecPieceCount)
		downLoadPieceSize := 0
		segmentSize := e.baseApp.PieceOp().SegmentPieceSize(task.GetObjectInfo().PayloadSize, task.GetSegmentIdx(), maxSegmentSize)
		for ecIdx := 0; ecIdx < int(ecPieceCount); ecIdx++ {
			recoveryDataSources[ecIdx] = nil
			go func(secondaryIndex int) {
				pieceData, err := e.doRecoveryPiece(ctx, task, secondaryEndpoints[secondaryIndex])
				if err == nil {
					recoveryDataSources[secondaryIndex] = pieceData
					log.Debugf("get one piece from ", "piece length:%d ", len(pieceData), "secondary sp:", secondaryEndpoints[secondaryIndex])
					doneCh <- true
				}
				// finish all the task, send signal to quitCh
				if atomic.AddInt32(&totalTaskNum, -1) == 0 {
					downLoadPieceSize = len(pieceData)
					quitCh <- true
				}
			}(ecIdx)
		}

	loop:
		for {
			select {
			case <-doneCh:
				doneTaskNum++
				// it is enough to recovery data with minRecoveryPieces EC data, no need to wait
				if doneTaskNum >= minRecoveryPieces {
					break loop
				}
			case <-quitCh: // all the task finish
				if doneTaskNum < minRecoveryPieces { // finish task num not enough
					log.CtxErrorw(ctx, "get piece from secondary not enough", "get secondary piece num:", doneTaskNum, "error", ErrRecoveryPieceNotEnough)
					task.SetError(ErrRecoveryPieceNotEnough)
					return
				}
				ecTotalSize := int64(uint32(downLoadPieceSize) * dataShards)
				if ecTotalSize < segmentSize || ecTotalSize > segmentSize+int64(dataShards) {
					log.CtxErrorw(ctx, "get secondary piece data length error")
					task.SetError(ErrRecoveryPieceLength)
					return
				}
			}
		}
		recoverySegData, err := redundancy.DecodeRawSegment(recoveryDataSources, segmentSize, int(dataShards), int(parityShards))
		if err != nil {
			log.CtxErrorw(ctx, "EC decode error when recovery", "objectName:", task.GetObjectInfo().ObjectName, "segIndex:", task.GetSegmentIdx(), "error", err)
			task.SetError(ErrRecoveryDecode)
			return
		}
		// compare integrity hash
		if err = e.checkRecoveryCheckSum(ctx, task, hash.GenerateChecksum(recoverySegData)); err != nil {
			return
		}

		recoveryKey = e.baseApp.PieceOp().SegmentPieceKey(task.GetObjectInfo().Id.Uint64(), task.GetSegmentIdx())
		// write the recovery segment key to keystore
		err = e.baseApp.PieceStore().PutPiece(ctx, recoveryKey, recoverySegData)
		if err != nil {
			log.CtxErrorw(ctx, "EC decode data write piece fail", "pieceKey:", recoveryKey, "error", err)
			return
		}
		finishRecovery = true
	}

	log.CtxDebugw(ctx, "primary SP recovery successfully", "pieceKey:", recoveryKey)
}

func (e *ExecuteModular) checkRecoveryCheckSum(ctx context.Context, task coretask.RecoveryPieceTask, recoveryChecksum []byte) error {
	integrityMeta, err := e.baseApp.GfSpDB().GetObjectIntegrity(task.GetObjectInfo().Id.Uint64())
	if err != nil {
		log.CtxErrorw(ctx, "search integrity hash in db error when recovery", "objectName:", task.GetObjectInfo().ObjectName, "error", err)
		task.SetError(ErrGfSpDB)
		return ErrGfSpDB
	}
	expectedHash := integrityMeta.PieceChecksumList[task.GetSegmentIdx()]
	if !bytes.Equal(recoveryChecksum, expectedHash) {
		log.CtxErrorw(ctx, "check integrity hash of recovery data err", "objectName:", task.GetObjectInfo().ObjectName,
			"expected value", hex.EncodeToString(expectedHash), "actual value", recoveryChecksum, "error", ErrRecoveryPieceChecksum)
		task.SetError(ErrRecoveryPieceChecksum)
		return ErrRecoveryPieceChecksum
	}
	return nil
}

func (e *ExecuteModular) doRecoveryPiece(ctx context.Context, rTask coretask.RecoveryPieceTask, endpoint string) (data []byte, err error) {
	var (
		signature []byte
		pieceData []byte
	)
	startTime := time.Now()
	defer func() {
		metrics.RecoverPieceTimeHistogram.WithLabelValues(e.Name()).Observe(time.Since(startTime).Seconds())
	}()

	signature, err = e.baseApp.GfSpClient().SignRecoveryTask(ctx, rTask)
	if err != nil {
		log.CtxErrorw(ctx, "failed to sign recovery task", "object", rTask.GetObjectInfo().GetObjectName(), "error", err)
		return
	}
	rTask.SetSignature(signature)
	// recovery primary sp segment pr secondary piece
	respBody, err := e.baseApp.GfSpClient().GetPieceFromECChunks(ctx, endpoint, rTask)
	if err != nil {
		log.CtxErrorw(ctx, "failed to recovery piece", "objectID", rTask.GetObjectInfo().Id,
			"segment_idx", rTask.GetSegmentIdx(), "secondary endpoint", endpoint, "error", err)
		return
	}

	defer respBody.Close()
	pieceData, err = io.ReadAll(respBody)
	if err != nil {
		log.CtxErrorw(ctx, "failed to read recovery piece data from sp", "objectID", rTask.GetObjectInfo().Id,
			"segment idx", rTask.GetSegmentIdx(), "secondary endpoint", endpoint, "error", err)
		return nil, err
	}

	log.CtxDebugw(ctx, "success to recovery piece from sp", "objectID", rTask.GetObjectInfo().Id,
		"segment_idx", rTask.GetSegmentIdx(), "secondary endpoint", endpoint)

	return pieceData, nil
}

// getObjectSecondaryEndpoints return the secondary sp endpoints list of the specific object
func (g *ExecuteModular) getObjectSecondaryEndpoints(ctx context.Context, objectInfo *storagetypes.ObjectInfo) ([]string, error) {
	secondarySPAddrs := objectInfo.GetSecondarySpAddresses()
	spList, err := g.baseApp.Consensus().ListSPs(ctx)
	if err != nil {
		return nil, err
	}

	secondaryEndpointList := make([]string, len(secondarySPAddrs))
	for idx, addr := range secondarySPAddrs {
		for _, info := range spList {
			if addr == info.GetOperatorAddress() {
				secondaryEndpointList[idx] = info.Endpoint
			}
		}
	}

	return secondaryEndpointList, nil
}

func (g *ExecuteModular) getObjectPrimarySPEndpoint(ctx context.Context, bucketName string) (string, error) {
	spList, err := g.baseApp.Consensus().ListSPs(ctx)
	if err != nil {
		return "", err
	}

	bucketInfo, err := g.baseApp.Consensus().QueryBucketInfo(ctx, bucketName)
	if err != nil {
		return "", err
	}

	primarySP := bucketInfo.GetPrimarySpAddress()
	for _, info := range spList {
		if primarySP == info.GetOperatorAddress() {
			return info.Endpoint, nil
		}
	}

	return "", ErrPrimaryNotFound
}
