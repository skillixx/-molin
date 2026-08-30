package video

import "context"

type SubmitWorker struct{ gateway *VideoGateway }
type PollWorker struct{ gateway *VideoGateway }
type AssetFetchWorker struct{ gateway *VideoGateway }

func NewSubmitWorker(gateway *VideoGateway) *SubmitWorker { return &SubmitWorker{gateway: gateway} }
func NewPollWorker(gateway *VideoGateway) *PollWorker     { return &PollWorker{gateway: gateway} }
func NewAssetFetchWorker(gateway *VideoGateway) *AssetFetchWorker {
	return &AssetFetchWorker{gateway: gateway}
}

func (w *SubmitWorker) Run(ctx context.Context, taskID string) (GatewayTask, error) {
	return w.gateway.Submit(ctx, taskID)
}
func (w *PollWorker) Run(ctx context.Context, taskID string) (GatewayTask, error) {
	return w.gateway.Poll(ctx, taskID)
}
func (w *AssetFetchWorker) Run(ctx context.Context, taskID string) (GatewayTask, error) {
	return w.gateway.FetchAndFinalize(ctx, taskID)
}
