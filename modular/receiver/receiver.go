package receiver

import (
	"context"

	"github.com/bnb-chain/greenfield-storage-provider/base/gfspapp"
	"github.com/bnb-chain/greenfield-storage-provider/core/module"
	"github.com/bnb-chain/greenfield-storage-provider/core/rcmgr"
	"github.com/bnb-chain/greenfield-storage-provider/core/taskqueue"
)

var _ module.Receiver = &ReceiveModular{}

type ReceiveModular struct {
	baseApp      *gfspapp.GfSpBaseApp
	scope        rcmgr.ResourceScope
	receiveQueue taskqueue.TQueueOnStrategy
}

func (r *ReceiveModular) Name() string {
	return module.ReceiveModularName
}

func (r *ReceiveModular) Start(ctx context.Context) error {
	scope, err := r.baseApp.ResourceManager().OpenService(r.Name())
	if err != nil {
		return err
	}
	r.scope = scope
	return nil
}

func (r *ReceiveModular) Stop(ctx context.Context) error {
	r.scope.Release()
	return nil
}

func (r *ReceiveModular) ReserveResource(
	ctx context.Context,
	state *rcmgr.ScopeStat) (
	rcmgr.ResourceScopeSpan, error) {
	span, err := r.scope.BeginSpan()
	if err != nil {
		return nil, err
	}
	err = span.ReserveResources(state)
	if err != nil {
		return nil, err
	}
	return span, nil
}

func (r *ReceiveModular) ReleaseResource(
	ctx context.Context,
	span rcmgr.ResourceScopeSpan) {
	span.Done()
}
