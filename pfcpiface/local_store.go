// SPDX-License-Identifier: Apache-2.0
// Copyright 2022-present Open Networking Foundation

package pfcpiface

import (
	"sync"
)

type InMemoryStore struct {
	// sessions stores all PFCP sessions.
	// sync.Map is optimized for case when multiple goroutines
	// read, write, and overwrite entries for disjoint sets of keys.
	sessions sync.Map
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

func (i *InMemoryStore) GetAllSessions() []PFCPSession {
	sessions := make([]PFCPSession, 0)

	i.sessions.Range(func(key, value interface{}) bool {
		v := value.(PFCPSession)
		sessions = append(sessions, v)
		return true
	})

	//log.WithFields(log.Fields{
	//	"sessions": sessions,
	//}).Trace("Got all PFCP sessions from local store")

	return sessions
}

func (i *InMemoryStore) PutSession(session PFCPSession) error {
	if session.localSEID == 0 {
		return ErrInvalidArgument("session.localSEID", session.localSEID)
	}

	i.sessions.Store(session.localSEID, session)

	//log.WithFields(log.Fields{
	//	"session": session,
	//}).Trace("Saved PFCP sessions to local store")

	return nil
}

//func (i *InMemoryStore) PutSessionBySMFKey(session PFCPSession) error {
//	if session.localSEID == 0 {
//		return ErrInvalidArgument("session.localSEID", session.localSEID)
//	}
//
//	i.sessions.Store(session.remoteSEID, session)
//
//	log.WithFields(log.Fields{
//		"session": session,
//	}).Trace("Saved PFCP sessions to local store")
//
//	return nil
//}

//func (i *InMemoryStore) UptoDownSEIDStore(upSEID, downSEID uint64) error {
//	if upSEID == 0 {
//		return ErrInvalidArgument("upSEID", upSEID)
//	}
//	if downSEID == 0 {
//		return ErrInvalidArgument("downSEID", downSEID)
//	}
//
//	i.sessions.Store(upSEID, downSEID)
//
//	log.WithFields(log.Fields{
//		"upSEID":   upSEID,
//		"downSEID": downSEID,
//	}).Trace("Saved smf seid to real seid map to local store")
//
//	return nil
//}

func (i *InMemoryStore) DeleteSession(fseid uint64) error {
	i.sessions.Delete(fseid)

	//log.WithFields(log.Fields{
	//	"F-SEID": fseid,
	//}).Trace("PFCP session removed from local store")

	return nil
}

func (i *InMemoryStore) DeleteAllSessions() bool {
	i.sessions.Range(func(key, value interface{}) bool {
		i.sessions.Delete(key)
		return true
	})

	//log.trace("All PFCP sessions removed from local store")

	return true
}

func (i *InMemoryStore) GetSession(fseid uint64) (PFCPSession, bool) {
	sess, ok := i.sessions.Load(fseid)
	if !ok {
		return PFCPSession{}, false
	}

	session, ok := sess.(PFCPSession)
	if !ok {
		return PFCPSession{}, false
	}

	//log.WithFields(log.Fields{
	//	"session": session,
	//}).Trace("Got PFCP session from local store")

	return session, ok
}
