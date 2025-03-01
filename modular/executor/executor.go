package executor

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/bnb-chain/greenfield-storage-provider/base/gfspapp"
	"github.com/bnb-chain/greenfield-storage-provider/base/types/gfsperrors"
	"github.com/bnb-chain/greenfield-storage-provider/base/types/gfsptask"
	"github.com/bnb-chain/greenfield-storage-provider/core/module"
	corercmgr "github.com/bnb-chain/greenfield-storage-provider/core/rcmgr"
	corespdb "github.com/bnb-chain/greenfield-storage-provider/core/spdb"
	coretask "github.com/bnb-chain/greenfield-storage-provider/core/task"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/metrics"
)

var _ module.TaskExecutor = &ExecuteModular{}

type ExecuteModular struct {
	baseApp *gfspapp.GfSpBaseApp
	scope   corercmgr.ResourceScope

	maxExecuteNum int64
	executingNum  int64

	askTaskInterval int

	askReplicateApprovalTimeout  int64
	askReplicateApprovalExFactor float64

	listenSealTimeoutHeight int
	listenSealRetryTimeout  int
	maxListenSealRetry      int

	statisticsOutputInterval   int
	doingReplicatePieceTaskCnt int64
	doingSpSealObjectTaskCnt   int64
	doingReceivePieceTaskCnt   int64
	doingGCObjectTaskCnt       int64
	doingGCZombiePieceTaskCnt  int64
	doingGCGCMetaTaskCnt       int64
	doingRecoveryPieceTaskCnt  int64
}

func (e *ExecuteModular) Name() string {
	return module.ExecuteModularName
}

func (e *ExecuteModular) Start(ctx context.Context) error {
	scope, err := e.baseApp.ResourceManager().OpenService(e.Name())
	if err != nil {
		return err
	}
	e.scope = scope
	go e.eventLoop(ctx)
	return nil
}

func (e *ExecuteModular) eventLoop(ctx context.Context) {
	for i := int64(0); i < e.maxExecuteNum; i++ {
		go func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
				default:
					err := e.AskTask(ctx)
					if err != nil {
						rand.New(rand.NewSource(time.Now().Unix()))
						sleep := rand.Intn(DefaultSleepInterval) + 1
						time.Sleep(time.Duration(sleep) * time.Millisecond)
					}
				}
			}
		}(ctx)
	}

	statisticsTicker := time.NewTicker(time.Duration(e.statisticsOutputInterval) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-statisticsTicker.C:
			log.CtxInfo(ctx, e.Statistics())
		}
	}
}

func (e *ExecuteModular) omitError(err error) bool {
	switch realErr := err.(type) {
	case *gfsperrors.GfSpError:
		if realErr.GetInnerCode() == gfspapp.ErrNoTaskMatchLimit.GetInnerCode() {
			return true
		}
	}
	return false
}

func (e *ExecuteModular) AskTask(ctx context.Context) error {
	atomic.AddInt64(&e.executingNum, 1)
	defer atomic.AddInt64(&e.executingNum, -1)

	limit, err := e.scope.RemainingResource()
	if err != nil {
		log.CtxErrorw(ctx, "failed to get remaining resource", "error", err)
		return err
	}

	metrics.RemainingMemoryGauge.WithLabelValues(e.Name()).Set(float64(limit.GetMemoryLimit()))
	metrics.RemainingTaskGauge.WithLabelValues(e.Name()).Set(float64(limit.GetTaskTotalLimit()))
	metrics.RemainingHighPriorityTaskGauge.WithLabelValues(e.Name()).Set(
		float64(limit.GetTaskLimit(corercmgr.ReserveTaskPriorityHigh)))
	metrics.RemainingMediumPriorityTaskGauge.WithLabelValues(e.Name()).Set(
		float64(limit.GetTaskLimit(corercmgr.ReserveTaskPriorityMedium)))
	metrics.RemainingLowTaskGauge.WithLabelValues(e.Name()).Set(
		float64(limit.GetTaskLimit(corercmgr.ReserveTaskPriorityLow)))

	askTask, err := e.baseApp.GfSpClient().AskTask(ctx, limit)
	if err != nil {
		if e.omitError(err) {
			return err
		}
		log.CtxErrorw(ctx, "failed to ask task", "remaining", limit.String(), "error", err)
		return err
	}
	// double confirm the safe task
	if askTask == nil {
		log.CtxErrorw(ctx, "failed to ask task due to dangling pointer",
			"remaining", limit.String(), "error", err)
		return ErrDanglingPointer
	}
	span, err := e.ReserveResource(ctx, askTask.EstimateLimit().ScopeStat())
	if err != nil {
		log.CtxErrorw(ctx, "failed to reserve resource", "task_require",
			askTask.EstimateLimit().String(), "remaining", limit.String(), "error", err)
		return err
	}
	metrics.RunningTaskNumberGauge.WithLabelValues("running_task_num").Set(float64(atomic.LoadInt64(&e.executingNum)))
	metrics.MaxTaskNumberGauge.WithLabelValues("max_task_num").Set(float64(atomic.LoadInt64(&e.executingNum)))
	defer e.ReleaseResource(ctx, span)
	defer e.ReportTask(ctx, askTask)
	ctx = log.WithValue(ctx, log.CtxKeyTask, askTask.Key().String())
	switch t := askTask.(type) {
	case *gfsptask.GfSpReplicatePieceTask:
		metrics.ExecutorReplicatePieceTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingReplicatePieceTaskCnt, 1)
		defer atomic.AddInt64(&e.doingReplicatePieceTaskCnt, -1)
		metrics.PerfUploadTimeHistogram.WithLabelValues("background_schedule_replicate_time").Observe(time.Since(time.Unix(t.GetCreateTime(), 0)).Seconds())
		e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorBeginTask, t.Key().String())
		e.HandleReplicatePieceTask(ctx, t)
		if t.Error() != nil {
			e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorEndTask, t.Key().String()+":"+t.Error().Error())
		}
		e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorEndTask, t.Key().String())
	case *gfsptask.GfSpSealObjectTask:
		metrics.ExecutorSealObjectTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingSpSealObjectTaskCnt, 1)
		defer atomic.AddInt64(&e.doingSpSealObjectTaskCnt, -1)
		e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorBeginTask, t.Key().String())
		e.HandleSealObjectTask(ctx, t)
		if t.Error() != nil {
			e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorEndTask, t.Key().String()+":"+t.Error().Error())
		}
		e.baseApp.GfSpDB().InsertUploadEvent(t.GetObjectInfo().Id.Uint64(), corespdb.ExecutorEndTask, t.Key().String())
	case *gfsptask.GfSpReceivePieceTask:
		metrics.ExecutorReceiveTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingReceivePieceTaskCnt, 1)
		defer atomic.AddInt64(&e.doingReceivePieceTaskCnt, -1)
		e.HandleReceivePieceTask(ctx, t)
	case *gfsptask.GfSpGCObjectTask:
		metrics.ExecutorGCObjectTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingGCObjectTaskCnt, 1)
		defer atomic.AddInt64(&e.doingGCObjectTaskCnt, -1)
		e.HandleGCObjectTask(ctx, t)
	case *gfsptask.GfSpGCZombiePieceTask:
		metrics.ExecutorGCZombieTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingGCZombiePieceTaskCnt, 1)
		defer atomic.AddInt64(&e.doingGCZombiePieceTaskCnt, -1)
		e.HandleGCZombiePieceTask(ctx, t)
	case *gfsptask.GfSpGCMetaTask:
		metrics.ExecutorGCMetaTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingGCGCMetaTaskCnt, 1)
		defer atomic.AddInt64(&e.doingGCGCMetaTaskCnt, -1)
		e.HandleGCMetaTask(ctx, t)
	case *gfsptask.GfSpRecoverPieceTask:
		metrics.ExecutorRecoveryTaskCounter.WithLabelValues(e.Name()).Inc()
		atomic.AddInt64(&e.doingRecoveryPieceTaskCnt, 1)
		defer atomic.AddInt64(&e.doingRecoveryPieceTaskCnt, 1)
		e.HandleRecoverPieceTask(ctx, t)
	default:
		log.CtxErrorw(ctx, "unsupported task type")
	}
	log.CtxDebugw(ctx, "finish to handle task")
	return nil
}

func (e *ExecuteModular) ReportTask(
	ctx context.Context,
	task coretask.Task) error {
	err := e.baseApp.GfSpClient().ReportTask(ctx, task)
	log.CtxDebugw(ctx, "finish to report task", "task_info", task.Info(), "error", err)
	return err
}

func (e *ExecuteModular) Stop(ctx context.Context) error {
	e.scope.Release()
	return nil
}

func (e *ExecuteModular) ReserveResource(
	ctx context.Context,
	st *corercmgr.ScopeStat) (
	corercmgr.ResourceScopeSpan, error) {
	span, err := e.scope.BeginSpan()
	if err != nil {
		return nil, err
	}
	err = span.ReserveResources(st)
	if err != nil {
		return nil, err
	}
	return span, nil
}

func (e *ExecuteModular) ReleaseResource(
	ctx context.Context,
	span corercmgr.ResourceScopeSpan) {
	span.Done()
}

func (e *ExecuteModular) Statistics() string {
	return fmt.Sprintf(
		"maxAsk[%d], asking[%d], replicate[%d], seal[%d], receive[%d], gcObject[%d], gcZombie[%d], gcMeta[%d]",
		&e.maxExecuteNum, atomic.LoadInt64(&e.executingNum),
		atomic.LoadInt64(&e.doingReplicatePieceTaskCnt),
		atomic.LoadInt64(&e.doingSpSealObjectTaskCnt),
		atomic.LoadInt64(&e.doingReceivePieceTaskCnt),
		atomic.LoadInt64(&e.doingGCObjectTaskCnt),
		atomic.LoadInt64(&e.doingGCZombiePieceTaskCnt),
		atomic.LoadInt64(&e.doingGCGCMetaTaskCnt))
}
