package manager

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bnb-chain/greenfield-storage-provider/base/gfspapp"
	"github.com/bnb-chain/greenfield-storage-provider/base/types/gfsptask"
	"github.com/bnb-chain/greenfield-storage-provider/core/module"
	"github.com/bnb-chain/greenfield-storage-provider/core/rcmgr"
	"github.com/bnb-chain/greenfield-storage-provider/core/spdb"
	"github.com/bnb-chain/greenfield-storage-provider/core/task"
	"github.com/bnb-chain/greenfield-storage-provider/core/taskqueue"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/metrics"
	"github.com/bnb-chain/greenfield-storage-provider/store/types"
	"github.com/bnb-chain/greenfield-storage-provider/util"
	storagetypes "github.com/bnb-chain/greenfield/x/storage/types"
)

const (
	// DiscontinueBucketReason defines the reason for stop serving
	DiscontinueBucketReason = "testnet cleanup"

	// DiscontinueBucketLimit define the max buckets to fetch in a single request
	DiscontinueBucketLimit = int64(500)

	// RejectUnSealObjectRetry defines the retry number of sending reject unseal object tx.
	RejectUnSealObjectRetry = 3

	// RejectUnSealObjectTimeout defines the timeout of sending reject unseal object tx.
	RejectUnSealObjectTimeout = 3
)

var _ module.Manager = &ManageModular{}

type ManageModular struct {
	baseApp *gfspapp.GfSpBaseApp
	scope   rcmgr.ResourceScope
	mux     sync.Mutex

	// loading task at startup.
	enableLoadTask           bool
	loadTaskLimitToReplicate int
	loadTaskLimitToSeal      int
	loadTaskLimitToGC        int

	uploadQueue           taskqueue.TQueueOnStrategy
	resumeableUploadQueue taskqueue.TQueueOnStrategy
	replicateQueue        taskqueue.TQueueOnStrategyWithLimit
	sealQueue             taskqueue.TQueueOnStrategyWithLimit
	receiveQueue          taskqueue.TQueueOnStrategyWithLimit
	gcObjectQueue         taskqueue.TQueueOnStrategyWithLimit
	gcZombieQueue         taskqueue.TQueueOnStrategyWithLimit
	gcMetaQueue           taskqueue.TQueueOnStrategyWithLimit
	downloadQueue         taskqueue.TQueueOnStrategy
	challengeQueue        taskqueue.TQueueOnStrategy
	recoveryQueue         taskqueue.TQueueOnStrategyWithLimit

	maxUploadObjectNumber int

	gcObjectTimeInterval  int
	gcBlockHeight         uint64
	gcObjectBlockInterval uint64
	gcSafeBlockDistance   uint64

	syncConsensusInfoInterval uint64
	statisticsOutputInterval  int

	discontinueBucketEnabled       bool
	discontinueBucketTimeInterval  int
	discontinueBucketKeepAliveDays int
}

func (m *ManageModular) Name() string {
	return module.ManageModularName
}

func (m *ManageModular) Start(ctx context.Context) error {
	m.uploadQueue.SetRetireTaskStrategy(m.GCUploadObjectQueue)
	m.resumeableUploadQueue.SetRetireTaskStrategy(m.GCResumableUploadObjectQueue)
	m.replicateQueue.SetRetireTaskStrategy(m.GCReplicatePieceQueue)
	m.replicateQueue.SetFilterTaskStrategy(m.FilterUploadingTask)
	m.sealQueue.SetRetireTaskStrategy(m.GCSealObjectQueue)
	m.sealQueue.SetFilterTaskStrategy(m.FilterUploadingTask)
	m.receiveQueue.SetRetireTaskStrategy(m.GCReceiveQueue)
	m.receiveQueue.SetFilterTaskStrategy(m.FilterUploadingTask)
	m.gcObjectQueue.SetRetireTaskStrategy(m.ResetGCObjectTask)
	m.gcObjectQueue.SetFilterTaskStrategy(m.FilterGCTask)
	m.downloadQueue.SetRetireTaskStrategy(m.GCCacheQueue)
	m.challengeQueue.SetRetireTaskStrategy(m.GCCacheQueue)
	m.recoveryQueue.SetRetireTaskStrategy(m.GCRecoverQueue)
	m.recoveryQueue.SetFilterTaskStrategy(m.FilterUploadingTask)

	scope, err := m.baseApp.ResourceManager().OpenService(m.Name())
	if err != nil {
		return err
	}
	m.scope = scope
	//err = m.LoadTaskFromDB()
	//if err != nil {
	//	return err
	//}

	go m.eventLoop(ctx)
	return nil
}

func (m *ManageModular) eventLoop(ctx context.Context) {
	m.syncConsensusInfo(ctx)
	gcObjectTicker := time.NewTicker(time.Duration(m.gcObjectTimeInterval) * time.Second)
	syncConsensusInfoTicker := time.NewTicker(time.Duration(m.syncConsensusInfoInterval) * time.Second)
	statisticsTicker := time.NewTicker(time.Duration(m.statisticsOutputInterval) * time.Second)
	discontinueBucketTicker := time.NewTicker(time.Duration(m.discontinueBucketTimeInterval) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-statisticsTicker.C:
			log.CtxDebug(ctx, m.Statistics())
		case <-syncConsensusInfoTicker.C:
			m.syncConsensusInfo(ctx)
		case <-gcObjectTicker.C:
			start := m.gcBlockHeight
			end := m.gcBlockHeight + m.gcObjectBlockInterval
			currentBlockHeight, err := m.baseApp.Consensus().CurrentHeight(ctx)
			if err != nil {
				log.CtxErrorw(ctx, "failed to get current block height for gc object and try again later", "error", err)
				continue
			}
			if end+m.gcSafeBlockDistance > currentBlockHeight {
				log.CtxErrorw(ctx, "current block number less safe distance and try again later",
					"start_gc_block_height", start,
					"end_gc_block_height", end,
					"safe_distance", m.gcSafeBlockDistance,
					"current_block_height", currentBlockHeight)
				continue
			}
			task := &gfsptask.GfSpGCObjectTask{}
			task.InitGCObjectTask(m.baseApp.TaskPriority(task), start, end, m.baseApp.TaskTimeout(task, 0))
			err = m.gcObjectQueue.Push(task)
			if err == nil {
				metrics.GCBlockNumberGauge.WithLabelValues(m.Name()).Set(float64(m.gcBlockHeight))
				m.gcBlockHeight = end + 1

				if err = m.baseApp.GfSpDB().InsertGCObjectProgress(task.Key().String(), &spdb.GCObjectMeta{
					StartBlockHeight: start,
					EndBlockHeight:   end,
				}); err != nil {
					log.CtxErrorw(ctx, "failed to init the gc object task", "error", err)
					continue
				}
			}
			log.CtxErrorw(ctx, "generate a gc object task", "task_info", task.Info(), "error", err)
		case <-discontinueBucketTicker.C:
			if !m.discontinueBucketEnabled {
				continue
			}
			m.discontinueBuckets(ctx)
			log.Infow("finished to discontinue buckets", "time", time.Now())
		}
	}
}

func (m *ManageModular) discontinueBuckets(ctx context.Context) {
	createAt := time.Now().AddDate(0, 0, -m.discontinueBucketKeepAliveDays)
	buckets, err := m.baseApp.GfSpClient().ListExpiredBucketsBySp(context.Background(),
		createAt.Unix(), m.baseApp.OperatorAddress(), DiscontinueBucketLimit)
	if err != nil {
		log.Errorw("failed to query expired buckets", "error", err)
		return
	}

	for _, bucket := range buckets {
		time.Sleep(1 * time.Second)
		log.Infow("start to discontinue bucket", "bucket_name", bucket.BucketInfo.BucketName)
		discontinueBucket := &storagetypes.MsgDiscontinueBucket{
			BucketName: bucket.BucketInfo.BucketName,
			Reason:     DiscontinueBucketReason,
		}
		err = m.baseApp.GfSpClient().DiscontinueBucket(ctx, discontinueBucket)
		if err != nil {
			log.Errorw("failed to discontinue bucket on chain", "bucket_name",
				discontinueBucket.BucketName, "error", err)
			continue
		} else {
			log.Infow("succeed to discontinue bucket", "bucket_name",
				discontinueBucket.BucketName)
		}
	}
}

func (m *ManageModular) Stop(ctx context.Context) error {
	m.scope.Release()
	return nil
}

func (m *ManageModular) ReserveResource(ctx context.Context, state *rcmgr.ScopeStat) (rcmgr.ResourceScopeSpan, error) {
	span, err := m.scope.BeginSpan()
	if err != nil {
		log.CtxErrorw(ctx, "failed to begin span", "error", err)
		return nil, err
	}
	err = span.ReserveResources(state)
	if err != nil {
		log.CtxErrorw(ctx, "failed to reserve resource", "error", err)
		return nil, err
	}
	return span, nil
}

func (m *ManageModular) ReleaseResource(ctx context.Context, span rcmgr.ResourceScopeSpan) {
	span.Done()
}

func (m *ManageModular) LoadTaskFromDB() error {
	if !m.enableLoadTask {
		log.Info("skip load tasks from db")
		return nil
	}

	var (
		err                          error
		replicateMetas               []*spdb.UploadObjectMeta
		generateReplicateTaskCounter int
		sealMetas                    []*spdb.UploadObjectMeta
		generateSealTaskCounter      int
		gcObjectMetas                []*spdb.GCObjectMeta
		generateGCOjectTaskCounter   int
	)

	log.Info("start to load task from sp db")

	replicateMetas, err = m.baseApp.GfSpDB().GetUploadMetasToReplicate(m.loadTaskLimitToReplicate)
	if err != nil {
		log.Errorw("failed to load replicate task from sp db", "error", err)
		return err
	}
	for _, meta := range replicateMetas {
		objectInfo, queryErr := m.baseApp.Consensus().QueryObjectInfoByID(context.Background(), util.Uint64ToString(meta.ObjectID))
		if queryErr != nil {
			log.Errorw("failed to query object info and continue", "object_id", meta.ObjectID, "error", queryErr)
			continue
		}

		if objectInfo.GetObjectStatus() != storagetypes.OBJECT_STATUS_CREATED {
			log.Infow("object is not in create status and continue", "object_info", objectInfo)
			continue
		}
		storageParams, queryErr := m.baseApp.Consensus().QueryStorageParamsByTimestamp(context.Background(), objectInfo.GetCreateAt())
		if queryErr != nil {
			log.Errorw("failed to query storage param and continue", "object_id", meta.ObjectID, "error", queryErr)
			continue
		}
		replicateTask := &gfsptask.GfSpReplicatePieceTask{}
		replicateTask.InitReplicatePieceTask(objectInfo, storageParams, m.baseApp.TaskPriority(replicateTask),
			m.baseApp.TaskTimeout(replicateTask, objectInfo.GetPayloadSize()), m.baseApp.TaskMaxRetry(replicateTask))
		pushErr := m.replicateQueue.Push(replicateTask)
		if pushErr != nil {
			log.Errorw("failed to push replicate piece task to queue", "object_info", objectInfo, "error", pushErr)
			continue
		}
		generateReplicateTaskCounter++
	}

	sealMetas, err = m.baseApp.GfSpDB().GetUploadMetasToSeal(m.loadTaskLimitToSeal)
	if err != nil {
		log.Errorw("failed to load seal task from sp db", "error", err)
		return err
	}
	for _, meta := range sealMetas {
		objectInfo, queryErr := m.baseApp.Consensus().QueryObjectInfoByID(context.Background(), util.Uint64ToString(meta.ObjectID))
		if queryErr != nil {
			log.Errorw("failed to query object info and continue", "object_id", meta.ObjectID, "error", queryErr)
			continue
		}
		if objectInfo.GetObjectStatus() != storagetypes.OBJECT_STATUS_CREATED {
			log.Infow("object is not in create status and continue", "object_info", objectInfo)
			continue
		}
		storageParams, queryErr := m.baseApp.Consensus().QueryStorageParamsByTimestamp(context.Background(), objectInfo.GetCreateAt())
		if queryErr != nil {
			log.Errorw("failed to query storage param and continue", "object_id", meta.ObjectID, "error", queryErr)
			continue
		}
		sealTask := &gfsptask.GfSpSealObjectTask{}
		sealTask.InitSealObjectTask(objectInfo, storageParams, m.baseApp.TaskPriority(sealTask), meta.SecondaryAddresses,
			meta.SecondarySignatures, m.baseApp.TaskTimeout(sealTask, 0), m.baseApp.TaskMaxRetry(sealTask))
		pushErr := m.sealQueue.Push(sealTask)
		if pushErr != nil {
			log.Errorw("failed to push seal object task to queue", "object_info", objectInfo, "error", pushErr)
			continue
		}
		generateSealTaskCounter++
	}

	gcObjectMetas, err = m.baseApp.GfSpDB().GetGCMetasToGC(m.loadTaskLimitToGC)
	if err != nil {
		log.Errorw("failed to load gc task from sp db", "error", err)
		return err
	}
	for _, meta := range gcObjectMetas {
		gcObjectTask := &gfsptask.GfSpGCObjectTask{}
		gcObjectTask.InitGCObjectTask(m.baseApp.TaskPriority(gcObjectTask), meta.StartBlockHeight, meta.EndBlockHeight, m.baseApp.TaskTimeout(gcObjectTask, 0))
		gcObjectTask.SetGCObjectProgress(meta.CurrentBlockHeight, meta.LastDeletedObjectID)
		pushErr := m.gcObjectQueue.Push(gcObjectTask)
		if pushErr != nil {
			log.Errorw("failed to push gc object task to queue", "gc_object_task_meta", meta, "error", pushErr)
			continue
		}
		generateGCOjectTaskCounter++
		if meta.EndBlockHeight >= m.gcBlockHeight {
			m.gcBlockHeight = meta.EndBlockHeight + 1
		}
	}

	log.Infow("end to load task from sp db", "replicate_task_number", generateReplicateTaskCounter,
		"seal_task_number", generateSealTaskCounter, "gc_object_task_number", generateGCOjectTaskCounter)
	return nil
}

func (m *ManageModular) TaskUploading(ctx context.Context, task task.Task) bool {
	if m.uploadQueue.Has(task.Key()) {
		log.CtxDebugw(ctx, "uploading object repeated")
		return true
	}
	if m.replicateQueue.Has(task.Key()) {
		log.CtxDebugw(ctx, "replicating object repeated")
		return true
	}
	if m.sealQueue.Has(task.Key()) {
		log.CtxDebugw(ctx, "sealing object repeated")
		return true
	}
	if m.resumeableUploadQueue.Has(task.Key()) {
		log.CtxDebugw(ctx, "resumable uploading object repeated")
		return true
	}
	return false
}

func (m *ManageModular) TaskRecovering(ctx context.Context, task task.Task) bool {
	if m.recoveryQueue.Has(task.Key()) {
		log.CtxDebugw(ctx, "recovery object repeated")
		return true
	}

	return false
}

func (m *ManageModular) UploadingObjectNumber() int {
	return m.uploadQueue.Len() + m.replicateQueue.Len() + m.sealQueue.Len() + m.resumeableUploadQueue.Len()
}

func (m *ManageModular) GCUploadObjectQueue(qTask task.Task) bool {
	task := qTask.(task.UploadObjectTask)
	if task.Expired() {
		go func() {
			if err := m.baseApp.GfSpDB().UpdateUploadProgress(&spdb.UploadObjectMeta{
				ObjectID:         task.GetObjectInfo().Id.Uint64(),
				TaskState:        types.TaskState_TASK_STATE_UPLOAD_OBJECT_ERROR,
				ErrorDescription: "expired",
			}); err != nil {
				log.Errorw("failed to update task state", "task_key", task.Key().String(), "error", err)
			}
		}()
		return true
	}
	return false
}

func (m *ManageModular) GCResumableUploadObjectQueue(qTask task.Task) bool {
	task := qTask.(task.ResumableUploadObjectTask)
	if task.Expired() {
		go func() {
			if err := m.baseApp.GfSpDB().UpdateUploadProgress(&spdb.UploadObjectMeta{
				ObjectID:         task.GetObjectInfo().Id.Uint64(),
				TaskState:        types.TaskState_TASK_STATE_UPLOAD_OBJECT_ERROR,
				ErrorDescription: "expired",
			}); err != nil {
				log.Errorw("failed to update task state", "task_key", task.Key().String(), "error", err)
			}
		}()
		return true
	}
	return false
}

func (m *ManageModular) GCReplicatePieceQueue(qTask task.Task) bool {
	task := qTask.(task.ReplicatePieceTask)
	if task.Expired() {
		go func() {
			if err := m.baseApp.GfSpDB().UpdateUploadProgress(&spdb.UploadObjectMeta{
				ObjectID:         task.GetObjectInfo().Id.Uint64(),
				TaskState:        types.TaskState_TASK_STATE_REPLICATE_OBJECT_ERROR,
				ErrorDescription: "expired",
			}); err != nil {
				log.Errorw("failed to update task state", "task_key", task.Key().String(), "error", err)
			}
		}()
		return true
	}
	return false
}

func (m *ManageModular) GCSealObjectQueue(qTask task.Task) bool {
	task := qTask.(task.SealObjectTask)
	if task.Expired() {
		go func() {
			if err := m.baseApp.GfSpDB().UpdateUploadProgress(&spdb.UploadObjectMeta{
				ObjectID:         task.GetObjectInfo().Id.Uint64(),
				TaskState:        types.TaskState_TASK_STATE_SEAL_OBJECT_ERROR,
				ErrorDescription: "expired",
			}); err != nil {
				log.Errorw("failed to update task state", "task_key", task.Key().String(), "error", err)
			}
		}()
		return true
	}
	return false
}

func (m *ManageModular) GCReceiveQueue(qTask task.Task) bool {
	return qTask.Expired()
}

func (m *ManageModular) GCRecoverQueue(qTask task.Task) bool {
	return qTask.ExceedRetry() || qTask.ExceedTimeout()
}

func (m *ManageModular) ResetGCObjectTask(qTask task.Task) bool {
	task := qTask.(task.GCObjectTask)
	if task.Expired() {
		log.Errorw("reset gc object task", "old_task_key", task.Key().String())
		task.SetRetry(0)
		log.Errorw("reset gc object task", "new_task_key", task.Key().String())
	}
	return false
}

func (m *ManageModular) GCCacheQueue(qTask task.Task) bool {
	return true
}

func (m *ManageModular) FilterGCTask(qTask task.Task) bool {
	return qTask.GetRetry() == 0
}

func (m *ManageModular) FilterUploadingTask(qTask task.Task) bool {
	if qTask.ExceedRetry() {
		return false
	}
	if qTask.ExceedTimeout() {
		return true
	}
	if qTask.GetRetry() == 0 {

		return true
	}
	return false
}

func (m *ManageModular) PickUpTask(ctx context.Context, tasks []task.Task) task.Task {
	if len(tasks) == 0 {
		return nil
	}
	if len(tasks) == 1 {
		log.CtxDebugw(ctx, "only one task for picking")
		return tasks[0]
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].GetPriority() < tasks[j].GetPriority()
	})
	var totalPriority int
	for _, task := range tasks {
		totalPriority += int(task.GetPriority())
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randPriority := r.Intn(totalPriority)
	totalPriority = 0

	for _, task := range tasks {
		totalPriority += int(task.GetPriority())
		if totalPriority >= randPriority {
			return task
		}
	}
	return nil
}

func (m *ManageModular) syncConsensusInfo(ctx context.Context) {
	spList, err := m.baseApp.Consensus().ListSPs(ctx)
	if err != nil {
		log.CtxErrorw(ctx, "failed to list sps", "error", err)
		return
	}
	if err = m.baseApp.GfSpDB().UpdateAllSp(spList); err != nil {
		log.CtxErrorw(ctx, "failed to update all sp list", "error", err)
		return
	}
	for _, sp := range spList {
		if strings.EqualFold(m.baseApp.OperatorAddress(), sp.OperatorAddress) {
			if err = m.baseApp.GfSpDB().SetOwnSpInfo(sp); err != nil {
				log.Errorw("failed to set own sp info", "error", err)
				return
			}
		}
	}
}

func (m *ManageModular) RejectUnSealObject(ctx context.Context, object *storagetypes.ObjectInfo) error {
	metrics.SealObjectFailedCounter.WithLabelValues(m.Name()).Inc()
	rejectUnSealObjectMsg := &storagetypes.MsgRejectSealObject{
		BucketName: object.GetBucketName(),
		ObjectName: object.GetObjectName(),
	}

	var err error
	for i := 0; i < RejectUnSealObjectRetry; i++ {
		err = m.baseApp.GfSpClient().RejectUnSealObject(ctx, rejectUnSealObjectMsg)
		if err != nil {
			time.Sleep(RejectUnSealObjectTimeout * time.Second)
		} else {
			log.CtxDebugw(ctx, "succeed to reject unseal object")
			reject, err := m.baseApp.Consensus().ListenRejectUnSealObject(ctx, object.Id.Uint64(), DefaultListenRejectUnSealTimeoutHeight)
			if err != nil {
				log.CtxErrorw(ctx, "failed to reject unseal object", "error", err)
				continue
			}
			if !reject {
				log.CtxErrorw(ctx, "failed to reject unseal object")
				continue
			}
			return nil
		}
	}
	log.CtxErrorw(ctx, "failed to reject unseal object", "error", err)
	return err
}

func (m *ManageModular) Statistics() string {
	return fmt.Sprintf(
		"upload[%d], replicate[%d], seal[%d], receive[%d], recovery[%d] gcObject[%d], gcZombie[%d], gcMeta[%d], download[%d], challenge[%d], gcBlockHeight[%d], gcSafeDistance[%d]",
		m.uploadQueue.Len(), m.replicateQueue.Len(), m.sealQueue.Len(),
		m.receiveQueue.Len(), m.recoveryQueue.Len(), m.gcObjectQueue.Len(), m.gcZombieQueue.Len(),
		m.gcMetaQueue.Len(), m.downloadQueue.Len(), m.challengeQueue.Len(),
		m.gcBlockHeight, m.gcSafeBlockDistance)
}
