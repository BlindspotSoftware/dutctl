// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildinfo

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// NewClientVersionRPCInterceptor returns the agent-side connect interceptor that
// guards the dutctl version contract. On every RPC it reads the client's build
// version from the request header and, if it is incompatible with version,
// rejects the call with connect.CodeFailedPrecondition before any module runs,
// logging the rejection to the logger in the request context. It also stamps
// version onto the response header so the client can run its own advisory
// check.
//
// version is the agent's own build version; pass [Version]. An empty version is
// tolerated (see [CompareVersions]): the guard then only advertises and never
// rejects.
func NewClientVersionRPCInterceptor(version string) connect.Interceptor {
	return &clientVersionInterceptor{version: version}
}

type clientVersionInterceptor struct {
	version string
}

// enforce compares the client version carried in header against the agent's own
// version and returns a CodeFailedPrecondition error when the pairing is
// incompatible, or nil when the call may proceed. A rejection is logged, as it
// happens before any handler runs and would otherwise leave no trace on the
// agent.
func (i *clientVersionInterceptor) enforce(ctx context.Context, header http.Header) error {
	peer := header.Get(VersionHeader)

	cmp := CompareVersions(i.version, peer)
	if cmp.Level != Incompatible {
		return nil
	}

	log.FromContext(ctx).Warn("rejected incompatible client",
		"agent", i.version, "client", peer, "reason", cmp.Reason)

	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("incompatible dutctl versions: agent %s, client %s (%s)", i.version, peer, cmp.Reason))
}

func (i *clientVersionInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		err := i.enforce(ctx, req.Header())
		if err != nil {
			return nil, err
		}

		res, err := next(ctx, req)
		if res != nil {
			res.Header().Set(VersionHeader, i.version)
		}

		return res, err
	}
}

func (i *clientVersionInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *clientVersionInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		conn.ResponseHeader().Set(VersionHeader, i.version)

		err := i.enforce(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}

		return next(ctx, conn)
	}
}

// NewServerVersionRPCInterceptor returns the client-side connect interceptor,
// the advisory sibling of [NewClientVersionRPCInterceptor]. On every RPC it
// stamps the client's build version onto the request header (so the agent can
// enforce it) and, once the agent's version arrives on the response header,
// logs it — and any drift — to the logger in the request context. The check is
// advisory only: it never fails a call. An incompatible pairing is rejected by
// the agent, whose error already explains the mismatch.
//
// version is the client's own build version; pass [Version]. An empty version is
// tolerated (see [CompareVersions]) and only ever warns.
func NewServerVersionRPCInterceptor(version string) connect.Interceptor {
	return &serverVersionInterceptor{version: version}
}

type serverVersionInterceptor struct {
	version string

	once sync.Once
}

// report classifies the agent version carried in header and logs the observed
// version plus any drift, at most once per client.
func (i *serverVersionInterceptor) report(l *slog.Logger, header http.Header) {
	i.once.Do(func() {
		peer := header.Get(VersionHeader)

		l.Info(fmt.Sprintf("dutagent version: %s", peer))

		cmp := CompareVersions(i.version, peer)
		if cmp.Level == Compatible {
			return
		}

		rel := "differs from"

		switch {
		case cmp.Delta < 0:
			rel = "is older than"
		case cmp.Delta > 0:
			rel = "is newer than"
		}

		l.Warn(fmt.Sprintf("dutctl client %s %s dutagent %s (%s)", i.version, rel, peer, cmp.Reason))
	})
}

func (i *serverVersionInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set(VersionHeader, i.version)

		res, err := next(ctx, req)
		if err != nil {
			return res, err
		}

		i.report(log.FromContext(ctx), res.Header())

		return res, nil
	}
}

func (i *serverVersionInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (i *serverVersionInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set(VersionHeader, i.version)

		return &reportingClientConn{
			StreamingClientConn: conn,
			interceptor:         i,
			logger:              log.FromContext(ctx),
		}
	}
}

// reportingClientConn runs the advisory report after the first response is
// received (which is when the response header becomes available). The
// interceptor's once guard makes the repeated calls cheap. The logger is taken
// from the call's context up front, as Receive has no context of its own.
type reportingClientConn struct {
	connect.StreamingClientConn

	interceptor *serverVersionInterceptor
	logger      *slog.Logger
}

func (c *reportingClientConn) Receive(msg any) error {
	err := c.StreamingClientConn.Receive(msg)
	if err != nil {
		return err
	}

	c.interceptor.report(c.logger, c.ResponseHeader())

	return nil
}
