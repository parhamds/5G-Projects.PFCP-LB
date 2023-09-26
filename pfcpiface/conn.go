// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Intel Corporation

package pfcpiface

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"sync"
	"time"

	reuse "github.com/libp2p/go-reuseport"
	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"

	"github.com/omec-project/upf-epc/pfcpiface/metrics"
)

const (
	UpPFCPPort   = "8805"
	DownPFCPPort = "8806"
	MaxItems     = 10
)

// Timeout : connection timeout.
var Timeout = 1000 * time.Millisecond

type sequenceNumber struct {
	seq uint32
	mux sync.Mutex
}

type recoveryTS struct {
	local  time.Time
	remote time.Time
}

type nodeID struct {
	localIE *ie.IE
	local   string
	remote  string
}

// PFCPConn represents a PFCP connection with a unique PFCP peer.
type PFCPConn struct {
	ctx context.Context
	// child socket for all subsequent packets from an "established PFCP connection"
	net.Conn
	ts         recoveryTS
	seqNum     sequenceNumber
	rng        *rand.Rand
	maxRetries int
	appPFDs    map[string]appPFD

	//localtoSMFstore SessionsStore
	//SMFtoRealstore  SessionsStore
	//smftoLocalstore SessionsStore
	//uptoDownstore   SessionsStore
	sessionStore SessionsStore

	nodeID nodeID
	upf    *Upf
	// channel to signal PFCPNode on exit
	done     chan<- string
	shutdown chan struct{}

	metrics.InstrumentPFCP

	hbReset     chan struct{}
	hbCtxCancel context.CancelFunc

	pendingReqs sync.Map
}

func (pConn *PFCPConn) startHeartBeatMonitor(comCh CommunicationChannel) {
	// Stop HeartBeat routine if already running
	if pConn.hbCtxCancel != nil {
		pConn.hbCtxCancel()
		pConn.hbCtxCancel = nil
	}

	hbCtx, hbCancel := context.WithCancel(pConn.ctx)
	pConn.hbCtxCancel = hbCancel

	//log.WithFields(log.Fields{
	//	"interval": pConn.upf.hbInterval,
	//}).Infoln("Starting Heartbeat timer")

	heartBeatExpiryTimer := time.NewTicker(pConn.upf.hbInterval)

	for {
		select {
		case <-hbCtx.Done():
			//log.infoln("Cancel HeartBeat Timer", pConn.RemoteAddr().String())
			heartBeatExpiryTimer.Stop()

			return
		case <-pConn.hbReset:
			heartBeatExpiryTimer.Reset(pConn.upf.hbInterval)
		case <-heartBeatExpiryTimer.C:
			////log.traceln("HeartBeat Interval Timer Expired", pConn.RemoteAddr().String())

			r := pConn.getHeartBeatRequest()

			if _, timeout := pConn.sendPFCPRequestMessage(r); timeout {
				heartBeatExpiryTimer.Stop()
				//fmt.Println("parham log : Shutdown called from startHeartBeatMonitor")
				pConn.Shutdown(comCh)
			}
		}
	}
}

// NewPFCPConn creates a connected UDP socket to the rAddr PFCP peer specified. (for the first association req msg)
// buf is the first message received from the peer, nil if we are initiating.
func (node *PFCPNode) NewPFCPConn(lAddr, rAddr string, buf []byte, comCh CommunicationChannel, pos Position) *PFCPConn {
	conn, err := reuse.Dial("udp", lAddr, rAddr)
	if err != nil {
		log.Errorln("dial socket failed", err)
	}

	ts := recoveryTS{
		local: time.Now(),
	}

	// TODO: Get SEID range from PFCPNode for this PFCPConn
	//log.infoln("Created PFCPConn from:", conn.LocalAddr(), "to:", conn.RemoteAddr())

	rng := rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404

	var p = &PFCPConn{
		ctx:        node.ctx,
		Conn:       conn,
		ts:         ts,
		rng:        rng,
		maxRetries: 100,
		//localtoSMFstore: NewInMemoryStore(),
		//SMFtoRealstore:  NewInMemoryStore(),
		//smftoLocalstore: NewInMemoryStore(),
		//uptoDownstore:   NewInMemoryStore(),
		sessionStore:   NewInMemoryStore(),
		upf:            node.upf,
		done:           node.pConnDone,
		shutdown:       make(chan struct{}),
		InstrumentPFCP: node.metrics,
		hbReset:        make(chan struct{}, 100),
		hbCtxCancel:    nil,
	}

	p.setLocalNodeID(node.upf.NodeID)

	if buf != nil {
		// TODO: Check if the first msg is Association Setup Request
		//fmt.Println("parham log: pause 10 min calling HandlePFCPMsg from NewPFCPConn func for UP")
		//time.Sleep(10 * time.Minute)
		//fmt.Println("parham log: calling HandlePFCPMsg from NewPFCPConn func")
		p.HandlePFCPMsg(buf, comCh, node)
	}

	// Update map of connections
	node.pConns.Store(rAddr, p)

	go p.Serve(comCh, node, pos)

	return p
}

func (pConn *PFCPConn) setLocalNodeID(id string) {
	nodeIP := net.ParseIP(id)

	// NodeID - FQDN
	if id != "" && nodeIP == nil {
		pConn.nodeID.localIE = ie.NewNodeID("", "", id)
		pConn.nodeID.local = id

		return
	}

	// NodeID provided is not an IP, use local address
	if nodeIP == nil {
		nodeIP = pConn.LocalAddr().(*net.UDPAddr).IP
	}

	pConn.nodeID.local = nodeIP.String()

	// NodeID - IPv4 vs IPv6
	if nodeIP.To4() != nil {
		pConn.nodeID.localIE = ie.NewNodeID(pConn.nodeID.local, "", "")
	} else {
		pConn.nodeID.localIE = ie.NewNodeID("", pConn.nodeID.local, "")
	}
}

// Serve serves forever a single PFCP peer.(exept first association req msg)
func (pConn *PFCPConn) Serve(comCh CommunicationChannel, node *PFCPNode, pos Position) {
	//fmt.Println("parham log : registered Read Timeout = ", pConn.upf.readTimeout)
	connTimeout := make(chan struct{}, 1)
	go func(connTimeout chan struct{}) {
		recvBuf := make([]byte, 65507) // Maximum UDP payload size

		for {
			err := pConn.SetReadDeadline(time.Now().Add(pConn.upf.readTimeout))
			if err != nil {
				log.Errorf("failed to set read timeout: %v", err)
			}

			n, err := pConn.Read(recvBuf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					//log.infof("Read timeout for connection %v<->%v, is the SMF still alive?",
					//pConn.LocalAddr(), pConn.RemoteAddr())
					connTimeout <- struct{}{}

					return
				}

				if errors.Is(err, net.ErrClosed) {
					return
				}

				continue
			}

			buf := append([]byte{}, recvBuf[:n]...)
			//fmt.Println("parham log: calling HandlePFCPMsg from Serve func")
			pConn.HandlePFCPMsg(buf, comCh, node)
		}
	}(connTimeout)

	// TODO: Sender goroutine

	for {
		select {
		case <-connTimeout:
			//fmt.Println("parham log : Shutdown called from connTimeout channel")
			if pos == Down {
				pConn.ShutdownForDown(node, comCh)
				return
			}
			pConn.Shutdown(comCh)
			return
		case <-pConn.ctx.Done():
			//fmt.Println("parham log : Shutdown called from ctx.Done channel")
			if pos == Down {
				pConn.ShutdownForDown(node, comCh)
				return
			}
			pConn.Shutdown(comCh)
			return

		case <-pConn.shutdown:
			return
		}
	}
}

// Shutdown stops connection backing PFCPConn.
func (pConn *PFCPConn) Shutdown(comCh CommunicationChannel) {
	close(pConn.shutdown)

	if pConn.hbCtxCancel != nil {
		pConn.hbCtxCancel()
		pConn.hbCtxCancel = nil
	}

	// Cleanup all sessions in this conn
	for _, sess := range pConn.sessionStore.GetAllSessions() {
		//pConn.upf.SendMsgToUPF(upfMsgTypeDel, sess.PacketForwardingRules, PacketForwardingRules{})
		pConn.RemoveSession(sess)
	}

	rAddr := pConn.RemoteAddr().String()
	pConn.done <- rAddr

	err := pConn.Close()
	if err != nil {
		log.Error("Failed to close PFCP connection..")
		return
	}

	//log.infoln("Shutdown complete for", rAddr)
	comCh.ResetSessions <- struct{}{}

}

func (node *PFCPNode) handleDeadUpf(upfIndex int) {
	//fmt.Println("parham log : start handling dead upf")
	//fmt.Println("parham log : node.upf.lbmap before reloadbalance = ", node.upf.lbmap)
	//fmt.Println("parham log : node.upf.upfsSessions before reloadbalance = ", node.upf.upfsSessions)
	if len(node.upf.peersUPF) > 1 {
		for i := 0; i < len(node.upf.upfsSessions)-1; i++ {
			sessions := node.upf.upfsSessions[upfIndex]
			node.reloadbalance(sessions, upfIndex)
		}
	}

	node.upf.upfsSessions = append(node.upf.upfsSessions[:upfIndex], node.upf.upfsSessions[upfIndex+1:]...)
	node.upf.peersIP = append(node.upf.peersIP[:upfIndex], node.upf.peersIP[upfIndex+1:]...)
	node.upf.peersUPF = append(node.upf.peersUPF[:upfIndex], node.upf.peersUPF[upfIndex+1:]...)
	for k, v := range node.upf.lbmap {
		if v > upfIndex {
			node.upf.lbmap[k] = v - 1
		}
	}
	//fmt.Println("parham log : node.upf.lbmap after reloadbalance = ", node.upf.lbmap)
	//fmt.Println("parham log : node.upf.upfsSessions after reloadbalance = ", node.upf.upfsSessions)
	//fmt.Println("parham log : done handling dead upf")
}

func (node *PFCPNode) reloadbalance(sessions []uint64, deadUpf int) {
	initupf := 0
	if deadUpf == 0 {
		initupf = 1
	}
	for _, v := range sessions {

		curUpf := initupf
		minSessions := len(node.upf.upfsSessions[curUpf])
		lightestUpf := curUpf
		if len(node.upf.upfsSessions) > 1 {
			for i := 0; i < len(node.upf.upfsSessions); i++ {
				if i == deadUpf {
					continue
				}
				if minSessions > len(node.upf.upfsSessions[i]) {
					minSessions = len(node.upf.upfsSessions[i])
					lightestUpf = i
				}
			}
		}
		node.upf.lbmap[v] = lightestUpf
		node.upf.upfsSessions[lightestUpf] = append(node.upf.upfsSessions[lightestUpf], v)

		sourceUpfIndex := deadUpf
		destUpfIndex := lightestUpf
		sourceAddr := node.upf.peersIP[sourceUpfIndex] + ":" + DownPFCPPort
		destAddr := node.upf.peersIP[destUpfIndex] + ":" + DownPFCPPort
		//fmt.Println("parham log : source upf ip = ", sourceAddr, " dest upf ip = ", destAddr)
		sourcePconn, ok := node.pConns.Load(sourceAddr)
		if !ok {
			//	fmt.Println("parham log : can not find source Pconn in node.pConns.Load(sourceAddr)")
			continue
		}
		destPconn, ok := node.pConns.Load(destAddr)
		if !ok {
			//	fmt.Println("parham log : can not find dest Pconn in node.pConns.Load(destAddr)")
			continue
		}
		sPconn := sourcePconn.(*PFCPConn)
		dPconn := destPconn.(*PFCPConn)
		//fmt.Println("parham log : geting session from dead upf")
		sess, ok := sPconn.sessionStore.GetSession(v)
		if !ok {
			//	fmt.Println("parham log : can not find sessioin in sPconn.sessionStore.GetSession(v)")
			continue
		}
		//fmt.Println("parham log : puting to lightest upf")
		dPconn.sessionStore.PutSession(sess)
	}
}

// Shutdown stops connection backing PFCPConn.
func (pConn *PFCPConn) ShutdownForDown(node *PFCPNode, comCh CommunicationChannel) {
	for i, u := range node.upf.peersUPF {
		if u.NodeID == pConn.nodeID.remote {
			node.handleDeadUpf(i)
			break
		}
	}
	close(pConn.shutdown)

	if pConn.hbCtxCancel != nil {
		pConn.hbCtxCancel()
		pConn.hbCtxCancel = nil
	}

	// Cleanup all sessions in this conn
	for _, sess := range pConn.sessionStore.GetAllSessions() {
		//pConn.upf.SendMsgToUPF(upfMsgTypeDel, sess.PacketForwardingRules, PacketForwardingRules{})
		estMsg, ok := node.upf.sesEstMsgStore[sess.localSEID]
		if ok {
			sesEstMsg := SesEstU2dMsg{
				msg:       estMsg,
				upSeid:    sess.localSEID,
				reforward: true,
			}
			comCh.SesEstU2d <- &sesEstMsg
		}

		pConn.RemoveSession(sess)
	}

	rAddr := pConn.RemoteAddr().String()
	pConn.done <- rAddr

	err := pConn.Close()
	if err != nil {
		log.Error("Failed to close PFCP connection..")
		return
	}

	//log.infoln("Shutdown complete for", rAddr)
}

func (pConn *PFCPConn) getSeqNum() uint32 {
	pConn.seqNum.mux.Lock()
	defer pConn.seqNum.mux.Unlock()
	pConn.seqNum.seq++

	return pConn.seqNum.seq
}
