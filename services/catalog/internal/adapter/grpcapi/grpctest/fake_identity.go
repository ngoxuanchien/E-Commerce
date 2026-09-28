package grpctest

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
)

// FakeIdentity records GrantRole calls and can be told to fail or stall.
type FakeIdentity struct {
	identityv1.UnimplementedIdentityInternalServer

	mu    sync.Mutex
	calls []*identityv1.GrantRoleRequest
	err   error
	delay time.Duration
}

// StartFakeIdentity serves a new FakeIdentity over bufconn.
func StartFakeIdentity(t testing.TB) (*FakeIdentity, *grpc.ClientConn) {
	t.Helper()
	f := &FakeIdentity{}
	conn := Serve(t, func(s *grpc.Server) { identityv1.RegisterIdentityInternalServer(s, f) })
	return f, conn
}

// SetError makes subsequent calls fail with err (nil restores success).
func (f *FakeIdentity) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// SetDelay makes subsequent calls wait d (or until the caller's deadline).
func (f *FakeIdentity) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// Calls returns copies of the requests received so far.
func (f *FakeIdentity) Calls() []*identityv1.GrantRoleRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*identityv1.GrantRoleRequest, len(f.calls))
	for i, c := range f.calls {
		out[i] = proto.Clone(c).(*identityv1.GrantRoleRequest)
	}
	return out
}

// GrantRole implements identityv1.IdentityInternalServer.
func (f *FakeIdentity) GrantRole(ctx context.Context, req *identityv1.GrantRoleRequest) (*identityv1.GrantRoleResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, proto.Clone(req).(*identityv1.GrantRoleRequest))
	err, delay := f.err, f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &identityv1.GrantRoleResponse{}, nil
}
