// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vtgate provides query routing rpc services
// for vttablets.
package vtgate

import (
	"fmt"
	"time"

	log "github.com/golang/glog"
	mproto "github.com/youtube/vitess/go/mysql/proto"
	"github.com/youtube/vitess/go/pools"
	rpcproto "github.com/youtube/vitess/go/rpcwrap/proto"
	tproto "github.com/youtube/vitess/go/vt/tabletserver/proto"
	"github.com/youtube/vitess/go/vt/vtgate/proto"
)

var RpcVTGate *VTGate

// VTGate is the rpc interface to vtgate. Only one instance
// can be created.
type VTGate struct {
	balancerMap    *BalancerMap
	tabletProtocol string
	connections    *pools.Numbered
	retryDelay     time.Duration
	retryCount     int
}

func Init(blm *BalancerMap, tabletProtocol string, retryDelay time.Duration, retryCount int) {
	if RpcVTGate != nil {
		log.Fatalf("VTGate already initialized")
	}
	RpcVTGate = &VTGate{
		balancerMap:    blm,
		tabletProtocol: tabletProtocol,
		connections:    pools.NewNumbered(),
		retryDelay:     retryDelay,
		retryCount:     retryCount,
	}
	proto.RegisterAuthenticated(RpcVTGate)
}

// GetSessionId is the first request sent by the client to begin a session. The returned
// id should be used for all subsequent communications.
func (vtg *VTGate) GetSessionId(sessionParams *proto.SessionParams, session *proto.Session) error {
	scatterConn := NewScatterConn(vtg.balancerMap, vtg.tabletProtocol, sessionParams.TabletType, vtg.retryDelay, vtg.retryCount)
	session.SessionId = scatterConn.Id
	vtg.connections.Register(scatterConn.Id, scatterConn)
	return nil
}

// ExecuteShard executes a non-streaming query on the specified shards.
func (vtg *VTGate) ExecuteShard(context *rpcproto.Context, query *proto.QueryShard, reply *mproto.QueryResult) error {
	scatterConn, err := vtg.connections.Get(query.SessionId)
	if err != nil {
		return fmt.Errorf("query: %s, session %d: %v", query.Sql, query.SessionId, err)
	}
	defer vtg.connections.Put(query.SessionId)
	qr, err := scatterConn.(*ScatterConn).Execute(query.Sql, query.BindVariables, query.Keyspace, query.Shards)
	if err == nil {
		*reply = *qr
	}
	return err
}

// ExecuteBatchShard executes a group of queries on the specified shards.
func (vtg *VTGate) ExecuteBatchShard(context *rpcproto.Context, batchQuery *proto.BatchQueryShard, reply *tproto.QueryResultList) error {
	scatterConn, err := vtg.connections.Get(batchQuery.SessionId)
	if err != nil {
		return fmt.Errorf("query: %v, session %d: %v", batchQuery.Queries, batchQuery.SessionId, err)
	}
	defer vtg.connections.Put(batchQuery.SessionId)
	qrs, err := scatterConn.(*ScatterConn).ExecuteBatch(batchQuery.Queries, batchQuery.Keyspace, batchQuery.Shards)
	if err == nil {
		*reply = *qrs
	}
	return err
}

// StreamExecuteShard executes a streaming query on the specified shards.
func (vtg *VTGate) StreamExecuteShard(context *rpcproto.Context, query *proto.QueryShard, sendReply func(interface{}) error) error {
	scatterConn, err := vtg.connections.Get(query.SessionId)
	if err != nil {
		return fmt.Errorf("query: %s, session %d: %v", query.Sql, query.SessionId, err)
	}
	defer vtg.connections.Put(query.SessionId)
	return scatterConn.(*ScatterConn).StreamExecute(query.Sql, query.BindVariables, query.Keyspace, query.Shards, sendReply)
}

// Begin begins a transaction. It has to be concluded by a Commit or Rollback.
func (vtg *VTGate) Begin(context *rpcproto.Context, session *proto.Session, noOutput *string) error {
	scatterConn, err := vtg.connections.Get(session.SessionId)
	if err != nil {
		return fmt.Errorf("session %d: %v", session.SessionId, err)
	}
	defer vtg.connections.Put(session.SessionId)
	return scatterConn.(*ScatterConn).Begin()
}

// Commit commits a transaction.
func (vtg *VTGate) Commit(context *rpcproto.Context, session *proto.Session, noOutput *string) error {
	scatterConn, err := vtg.connections.Get(session.SessionId)
	if err != nil {
		return fmt.Errorf("session %d: %v", session.SessionId, err)
	}
	defer vtg.connections.Put(session.SessionId)
	return scatterConn.(*ScatterConn).Commit()
}

// Rollback rolls back a transaction.
func (vtg *VTGate) Rollback(context *rpcproto.Context, session *proto.Session, noOutput *string) error {
	scatterConn, err := vtg.connections.Get(session.SessionId)
	if err != nil {
		return fmt.Errorf("session %d: %v", session.SessionId, err)
	}
	defer vtg.connections.Put(session.SessionId)
	return scatterConn.(*ScatterConn).Rollback()
}

// CloseSession closes the current session and releases all associated resources for the session.
func (vtg *VTGate) CloseSession(context *rpcproto.Context, session *proto.Session, noOutput *string) error {
	scatterConn, err := vtg.connections.Get(session.SessionId)
	if err != nil {
		return nil
	}
	defer vtg.connections.Unregister(session.SessionId)
	scatterConn.(*ScatterConn).Close()
	return nil
}
