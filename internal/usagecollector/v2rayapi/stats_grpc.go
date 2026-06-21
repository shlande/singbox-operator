package v2rayapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StatsServiceClient is the gRPC client-side interface for sing-box's
// v2rayapi StatsService.
type StatsServiceClient interface {
	GetStats(ctx context.Context, in *GetStatsRequest, opts ...grpc.CallOption) (*GetStatsResponse, error)
	QueryStats(ctx context.Context, in *QueryStatsRequest, opts ...grpc.CallOption) (*QueryStatsResponse, error)
	GetSysStats(ctx context.Context, in *SysStatsRequest, opts ...grpc.CallOption) (*SysStatsResponse, error)
}

// StatsServiceServer is the gRPC server-side interface that a fake/mock
// server must implement.
type StatsServiceServer interface {
	GetStats(context.Context, *GetStatsRequest) (*GetStatsResponse, error)
	QueryStats(context.Context, *QueryStatsRequest) (*QueryStatsResponse, error)
	GetSysStats(context.Context, *SysStatsRequest) (*SysStatsResponse, error)
}

// RegisterStatsServiceServer registers a server implementation with the
// gRPC service registrar.
func RegisterStatsServiceServer(s grpc.ServiceRegistrar, srv StatsServiceServer) {
	s.RegisterService(&StatsService_ServiceDesc, srv)
}

// UnimplementedStatsServiceServer may be embedded by fake/mock servers
// to satisfy the interface without implementing every method.
type UnimplementedStatsServiceServer struct{}

func (UnimplementedStatsServiceServer) GetStats(context.Context, *GetStatsRequest) (*GetStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetStats not implemented")
}
func (UnimplementedStatsServiceServer) QueryStats(context.Context, *QueryStatsRequest) (*QueryStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method QueryStats not implemented")
}
func (UnimplementedStatsServiceServer) GetSysStats(context.Context, *SysStatsRequest) (*SysStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetSysStats not implemented")
}

// StatsService_ServiceDesc is a minimal descriptor needed for
// grpc.RegisterService.  The upstream service name is set at init time to
// "v2ray.core.app.stats.command.StatsService".
var StatsService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "v2ray.core.app.stats.command.StatsService",
	HandlerType: (*StatsServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetStats",
			Handler:    handlerGetStats,
		},
		{
			MethodName: "QueryStats",
			Handler:    handlerQueryStats,
		},
		{
			MethodName: "GetSysStats",
			Handler:    handlerGetSysStats,
		},
	},
	Streams: []grpc.StreamDesc{},
}

// handlerGetStats decodes GetStatsRequest, dispatches to server, encodes response.
func handlerGetStats(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(GetStatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(StatsServiceServer).GetStats(ctx, in)
}

// handlerQueryStats decodes QueryStatsRequest, dispatches to server, encodes response.
func handlerQueryStats(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(QueryStatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(StatsServiceServer).QueryStats(ctx, in)
}

// handlerGetSysStats decodes SysStatsRequest, dispatches to server, encodes response.
func handlerGetSysStats(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	in := new(SysStatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(StatsServiceServer).GetSysStats(ctx, in)
}

// ---------------------------------------------------------------------------
// Real gRPC client
// ---------------------------------------------------------------------------

const (
	statsServiceGetStatsFullMethod    = "/v2ray.core.app.stats.command.StatsService/GetStats"
	statsServiceQueryStatsFullMethod  = "/v2ray.core.app.stats.command.StatsService/QueryStats"
	statsServiceGetSysStatsFullMethod = "/v2ray.core.app.stats.command.StatsService/GetSysStats"
)

type statsServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewStatsServiceClient returns a StatsServiceClient backed by the
// supplied connection.
func NewStatsServiceClient(cc grpc.ClientConnInterface) StatsServiceClient {
	return &statsServiceClient{cc: cc}
}

func (c *statsServiceClient) GetStats(ctx context.Context, in *GetStatsRequest, opts ...grpc.CallOption) (*GetStatsResponse, error) {
	out := new(GetStatsResponse)
	err := c.cc.Invoke(ctx, statsServiceGetStatsFullMethod, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *statsServiceClient) QueryStats(ctx context.Context, in *QueryStatsRequest, opts ...grpc.CallOption) (*QueryStatsResponse, error) {
	out := new(QueryStatsResponse)
	err := c.cc.Invoke(ctx, statsServiceQueryStatsFullMethod, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *statsServiceClient) GetSysStats(ctx context.Context, in *SysStatsRequest, opts ...grpc.CallOption) (*SysStatsResponse, error) {
	out := new(SysStatsResponse)
	err := c.cc.Invoke(ctx, statsServiceGetSysStatsFullMethod, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}
