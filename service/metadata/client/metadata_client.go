package client

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
	metatypes "github.com/bnb-chain/greenfield-storage-provider/service/metadata/types"
)

// MetadataClient is an metadata gRPC service client wrapper
type MetadataClient struct {
	address  string
	metadata metatypes.MetadataServiceClient
	conn     *grpc.ClientConn
}

// NewMetadataClient return an MetadataClient instance
func NewMetadataClient(address string) (*MetadataClient, error) {
	conn, err := grpc.DialContext(context.Background(), address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Errorw("failed to dial metadata service", "error", err)
		return nil, err
	}
	client := &MetadataClient{
		address:  address,
		conn:     conn,
		metadata: metatypes.NewMetadataServiceClient(conn),
	}
	return client, nil
}

// Close the metadata gPRC client connection
func (client *MetadataClient) Close() error {
	return client.conn.Close()
}

// GetUserBuckets get buckets info by a user address
func (client *MetadataClient) GetUserBuckets(ctx context.Context, in *metatypes.MetadataServiceGetUserBucketsRequest, opts ...grpc.CallOption) (*metatypes.MetadataServiceGetUserBucketsResponse, error) {
	resp, err := client.metadata.GetUserBuckets(ctx, in, opts...)
	ctx = log.Context(ctx, resp)
	if err != nil {
		log.CtxErrorw(ctx, "failed to send get user buckets rpc", "error", err)
		return nil, err
	}
	return resp, nil
}

// ListObjectsByBucketName list objects info by a bucket name
func (client *MetadataClient) ListObjectsByBucketName(ctx context.Context, in *metatypes.MetadataServiceListObjectsByBucketNameRequest, opts ...grpc.CallOption) (*metatypes.MetadataServiceListObjectsByBucketNameResponse, error) {
	resp, err := client.metadata.ListObjectsByBucketName(ctx, in, opts...)
	ctx = log.Context(ctx, resp)
	if err != nil {
		log.CtxErrorw(ctx, "failed to send list objects by bucket name rpc", "error", err)
		return nil, err
	}
	return resp, nil
}
