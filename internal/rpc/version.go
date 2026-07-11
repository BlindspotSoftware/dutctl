// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/compat"
	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// VersionHeader is the header both sides use to advertise their dutctl build
// version on every RPC: clients stamp it on the request, agents on the response.
const VersionHeader = "X-Dutctl-Version"

// reason renders a human-facing phrase for a compatibility result, derived from
// the structural fields of the comparison. It lives here — with its only
// callers — rather than in package compat, which stays free of message prose.
func reason(r compat.Result) string {
	if !r.Valid {
		return "versions not comparable"
	}

	switch r.Field {
	case compat.Major:
		return "major version mismatch"
	case compat.Minor:
		return "minor version mismatch"
	case compat.Prerelease:
		return "pre-release version mismatch"
	case compat.Patch, compat.Equal:
		return ""
	default:
		return ""
	}
}

// NewVersionEnforcer returns the agent-side connect interceptor that guards the
// dutctl version contract. On every RPC it reads the client's build version
// from the request header and, if it is incompatible with version, rejects the
// call with connect.CodeFailedPrecondition before any module runs, logging the
// rejection to the logger in the request context. It also stamps version onto
// the response header so the client can run its own advisory check.
//
// version is the agent's own build version; pass buildinfo.Version. An empty
// version is tolerated (see [compat.Compare]): the guard then only advertises
// and never rejects.
func NewVersionEnforcer(version string) connect.Interceptor {
	return &versionEnforcer{version: version}
}

type versionEnforcer struct {
	version string
}

// enforce compares the client version carried in header against the agent's own
// version and returns a CodeFailedPrecondition error when the pairing is
// incompatible, or nil when the call may proceed. A rejection is logged, as it
// happens before any handler runs and would otherwise leave no trace on the
// agent.
func (i *versionEnforcer) enforce(ctx context.Context, header http.Header) error {
	peer := header.Get(VersionHeader)

	cmp := compat.Compare(i.version, peer)
	if cmp.Verdict != compat.Incompatible {
		return nil
	}

	log.FromContext(ctx).Warn("rejected incompatible client",
		"agent", i.version, "client", peer, "reason", reason(cmp))

	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("incompatible dutctl versions: agent %s, client %s (%s)", i.version, peer, reason(cmp)))
}

func (i *versionEnforcer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
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

func (i *versionEnforcer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *versionEnforcer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		conn.ResponseHeader().Set(VersionHeader, i.version)

		err := i.enforce(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}

		return next(ctx, conn)
	}
}

// NewVersionAdvisor returns the client-side connect interceptor, the advisory
// sibling of [NewVersionEnforcer]. On every RPC it stamps the client's build
// version onto the request header (so the agent can enforce it) and, once the
// agent's version arrives on the response header, logs it — and any drift — to
// the logger in the request context. The check is advisory only: it never fails
// a call. An incompatible pairing is rejected by the agent, whose error already
// explains the mismatch.
//
// version is the client's own build version; pass buildinfo.Version. An empty
// version is tolerated (see [compat.Compare]) and only ever warns.
func NewVersionAdvisor(version string) connect.Interceptor {
	return &versionAdvisor{version: version}
}

type versionAdvisor struct {
	version string

	once sync.Once
}

// report classifies the agent version carried in header and logs the observed
// version plus any drift, at most once per client.
func (i *versionAdvisor) report(l *slog.Logger, header http.Header) {
	i.once.Do(func() {
		peer := header.Get(VersionHeader)

		l.Info(fmt.Sprintf("dutagent version: %s", peer))

		cmp := compat.Compare(i.version, peer)
		if cmp.Verdict == compat.Compatible {
			return
		}

		rel := "differs from"

		switch {
		case cmp.Cmp < 0:
			rel = "is older than"
		case cmp.Cmp > 0:
			rel = "is newer than"
		}

		l.Warn(fmt.Sprintf("dutctl client %s %s dutagent %s (%s)", i.version, rel, peer, reason(cmp)))
	})
}

func (i *versionAdvisor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
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

func (i *versionAdvisor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (i *versionAdvisor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
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

	interceptor *versionAdvisor
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
