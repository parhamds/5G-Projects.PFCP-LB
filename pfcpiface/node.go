// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Intel Corporation
// Copyright 2021 Open Networking Foundation
package pfcpiface

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	reuse "github.com/libp2p/go-reuseport"
	"github.com/omec-project/upf-epc/pfcpiface/metrics"
	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// PFCPNode represents a PFCP endpoint of the UPF.
type PFCPNode struct {
	ctx    context.Context
	cancel context.CancelFunc
	// listening socket for new "PFCP connections"
	net.PacketConn
	// done is closed to signal shutdown complete
	done chan struct{}
	// channel for PFCPConn to signal exit by sending their remote address
	pConnDone chan string
	// map of existing connections
	pConns sync.Map
	// upf
	upf *Upf
	// metrics for PFCP messages and sessions
	metrics metrics.InstrumentPFCP
}

// NewPFCPNode create a new PFCPNode listening on local address.
func NewPFCPNode(pos Position, upf *Upf) *PFCPNode {
	var conn net.PacketConn
	var err error

	if pos == Up {
		//fmt.Println("parham log: calling ListenPacket for up")
		conn, err = reuse.ListenPacket("udp", ":"+UpPFCPPort)
		//fmt.Println("parham log: done ListenPacket for up")
	} else {

		//fmt.Println("parham log: calling ListenPacket for down")
		conn, err = reuse.ListenPacket("udp", ":"+DownPFCPPort)
		//fmt.Println("parham log: done ListenPacket for down")
	}
	if err != nil {
		log.Fatalln("ListenUDP failed", err)
	}

	//metrics, err := metrics.NewPrometheusService()
	if err != nil {
		log.Fatalln("prom metrics service init failed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &PFCPNode{
		ctx:        ctx,
		cancel:     cancel,
		PacketConn: conn,
		done:       make(chan struct{}),
		pConnDone:  make(chan string, 100),
		upf:        upf,
		//metrics:    metrics,
	}
}

func (node *PFCPNode) tryConnectToN4Peer(lAddrStr string, comCh CommunicationChannel, pfcpinfo PfcpInfo, pos Position) {
	//fmt.Println("parham log : start tryConnectToN4Peers func")

	conn, err := net.Dial("udp", pfcpinfo.Ip+":"+DownPFCPPort)
	if err != nil {
		log.Warnln("Failed to establish PFCP connection to peer ", pfcpinfo.Ip)
		return
	}

	remoteAddr := conn.RemoteAddr().(*net.UDPAddr)
	n4DstIP := remoteAddr.IP

	//log.WithFields(log.Fields{
	//	"SPGWC/SMF host": pfcpinfo.Ip,
	//	"CP node":        n4DstIP.String(),
	//}).Info("Establishing PFCP Conn with CP node")
	//fmt.Println("parham log : call NewPFCPConn from tryConnectToN4Peers func for down")
	pfcpConn := node.NewPFCPConn(lAddrStr, n4DstIP.String()+":"+DownPFCPPort, nil, comCh, pos)
	if pfcpConn != nil {

		go pfcpConn.sendAssociationRequest(pfcpinfo, comCh, node)
	}

}

func (node *PFCPNode) pfcpMsgLBer(seid uint64) int {

	upfIndex, ok := node.upf.lbmap[seid]
	if ok {
		return upfIndex
	}

	minSessions := len(node.upf.upfsSessions[0])
	lightestUpf := 0
	if len(node.upf.upfsSessions) > 1 {
		for i := 1; i < len(node.upf.upfsSessions); i++ {
			if minSessions > len(node.upf.upfsSessions[i]) {
				minSessions = len(node.upf.upfsSessions[i])
				lightestUpf = i
			}
		}
	}
	node.upf.lbmap[seid] = lightestUpf
	node.upf.upfsSessions[lightestUpf] = append(node.upf.upfsSessions[lightestUpf], seid)
	fmt.Println("pfcpMsgLBer called, node.upf.upfsSessions = ", node.upf.upfsSessions)
	for i := 0; i < len(node.upf.upfsSessions[lightestUpf]); i++ {
		fmt.Printf("len(node.upf.upfsSessions[%v]) = %v", i, len(node.upf.upfsSessions[i]))
	}

	//fmt.Println("parham log : node.upf.lbmap = ", node.upf.lbmap)
	//fmt.Println("parham log : node.upf.upfsSessions = ", node.upf.upfsSessions)
	return lightestUpf
}

func (node *PFCPNode) listenForSesEstReq(comCh CommunicationChannel) {
	for {
		//fmt.Println("parham log : down is waiting for new session establishment req from up ...")
		sereqMsg := <-comCh.SesEstU2d

		sereq := sereqMsg.msg
		var respCh chan *ie.IE
		if !sereqMsg.reforward {
			respCh = sereqMsg.respCh

		}

		//fmt.Println("parham log: ses est recieved by down : upseid = ", sereqMsg.upSeid)
		upfIndex := node.pfcpMsgLBer(sereqMsg.upSeid)
		fmt.Println("ses est received by down, up seid = ", sereqMsg.upSeid, ", upfIndex = ", upfIndex, ", reforward= ", sereqMsg.reforward)
		//fmt.Println("parham log: selected upfIndex = ", upfIndex)
		rAddr := node.upf.peersIP[upfIndex] + ":" + DownPFCPPort
		v, ok := node.pConns.Load(rAddr)
		if !ok {
			//log.infoln("Can't find pConn to received peer IP = ", node.upf.peersIP[upfIndex])
			if !sereqMsg.reforward {
				respCh <- ie.NewCause(ie.CauseRequestRejected)
			}
			continue
		}
		pConn := v.(*PFCPConn)
		sereq.NodeID = pConn.nodeID.localIE
		//fseid, err := sereq.CPFSEID.FSEID()
		//remoteSEID := fseid.SEID
		if !sereqMsg.reforward {
			session, ok := pConn.NewPFCPSessionForDown(sereqMsg.upSeid)
			if !ok {
				log.Errorf("can not create session in down:")
				if !sereqMsg.reforward {
					respCh <- ie.NewCause(ie.CauseRequestRejected)
				}
				continue
			}

			err := pConn.sessionStore.PutSession(session)
			if err != nil {
				log.Errorf("Failed to put PFCP session to store: %v", err)
				if !sereqMsg.reforward {
					respCh <- ie.NewCause(ie.CauseRequestRejected)
				}
				continue
			}
			//fmt.Println("parham log : session succesfully added to sessionStore, down = ", session.localSEID, " , up = ", session.remoteSEID)
		}
		var localFSEID *ie.IE

		localIP := pConn.LocalAddr().(*net.UDPAddr).IP
		if localIP.To4() != nil {
			localFSEID = ie.NewFSEID(sereqMsg.upSeid, localIP, nil)
		} else {
			localFSEID = ie.NewFSEID(sereqMsg.upSeid, nil, localIP)
		}
		sereq.CPFSEID = localFSEID
		if sereqMsg.reforward == true {
			sereq.Header.MessagePriority = 123
		} else {
			node.upf.sesEstMsgStore[sereqMsg.upSeid] = sereq
		}
		if !sereqMsg.reforward {
			pConn.upf.seidToRespCh[sereqMsg.upSeid] = respCh
		}
		fmt.Println("sending ses est to Real PFCP")
		pConn.forwardToRealPFCP(sereq, comCh)

	}
}

func (node *PFCPNode) listenForSesModReq(comCh CommunicationChannel) {
	for {
		//fmt.Println("parham log : down is waiting for new session modification req from up ...")
		smreqMsg := <-comCh.SesModU2d
		smreq := smreqMsg.msg
		var respCh chan *ie.IE
		if !smreqMsg.reforward {
			respCh = smreqMsg.respCh
		}

		//fmt.Println("parham log: ses mod recieved by down : upseid = ", smreqMsg.upSeid)
		upfIndex := node.pfcpMsgLBer(smreqMsg.upSeid)
		fmt.Println("ses est received by down, up seid = ", smreqMsg.upSeid, ", upfIndex = ", upfIndex, ", reforward= ", smreqMsg.reforward)
		//fmt.Println("parham log: selected upfIndex = ", upfIndex)
		rAddr := node.upf.peersIP[upfIndex] + ":" + DownPFCPPort
		v, ok := node.pConns.Load(rAddr)
		if !ok {
			//log.infoln("Can't find pConn to received peer IP = ", node.upf.peersIP[upfIndex])
			if !smreqMsg.reforward {
				respCh <- ie.NewCause(ie.CauseRequestRejected)
			}
			continue
		}
		pConn := v.(*PFCPConn)
		//smreq.NodeID = pConn.nodeID.localIE

		//fseid, err := smreq.CPFSEID.FSEID()
		//if err != nil {
		//	log.Errorln("can not read smf seid from smreq in down")
		//	comCh.SesModRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		//	continue
		//}

		//session, ok := pConn.smftoLocalstore.GetSession(fseid.SEID)
		//if !ok {
		//	log.Errorln("can not find smf seid in smftoLocalstore, smf seid = ", fseid.SEID)
		//	comCh.SesModRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		//	continue
		//}

		var localFSEID *ie.IE

		localIP := pConn.LocalAddr().(*net.UDPAddr).IP
		if localIP.To4() != nil {
			localFSEID = ie.NewFSEID(smreqMsg.upSeid, localIP, nil)
		} else {
			localFSEID = ie.NewFSEID(smreqMsg.upSeid, nil, localIP)
		}
		smreq.CPFSEID = localFSEID

		smreq.Header.SEID = smreqMsg.upSeid
		//fmt.Println("parham log : send session modification req from up to real in down")
		if smreqMsg.reforward == true {
			smreq.Header.MessagePriority = 123
		} else {
			node.upf.sesModMsgStore[smreqMsg.upSeid] = smreq
		}
		if !smreqMsg.reforward {
			pConn.upf.seidToRespCh[smreqMsg.upSeid] = respCh
		}
		fmt.Println("sending ses mod to Real PFCP")
		pConn.forwardToRealPFCP(smreq, comCh)

	}
}

func (node *PFCPNode) listenForSesDelReq(comCh CommunicationChannel) {
	for {
		//fmt.Println("parham log : down is waiting for new session deletion req from up ...")
		sdreqMsg := <-comCh.SesDelU2d
		sdreq := sdreqMsg.msg
		var respCh chan *ie.IE
		if !sdreqMsg.reforward {
			respCh = sdreqMsg.respCh
		}

		//fmt.Println("parham log: ses del recieved : upseid = ", sdreqMsg.upSeid)
		var upfIndex int
		var pConn *PFCPConn
		if sdreqMsg.reforward {
			upfIndex = sdreqMsg.upfIndex
			fmt.Println("ses est received by down, up seid = ", sdreqMsg.upSeid, ", upfIndex = ", upfIndex, ", reforward= ", sdreqMsg.reforward)
			pConn = sdreqMsg.pConn
		} else {
			upfIndex = node.pfcpMsgLBer(sdreqMsg.upSeid)
			fmt.Println("ses est received by down, up seid = ", sdreqMsg.upSeid, ", upfIndex = ", upfIndex, ", reforward= ", sdreqMsg.reforward)
			rAddr := node.upf.peersIP[upfIndex] + ":" + DownPFCPPort
			v, ok := node.pConns.Load(rAddr)
			if !ok {
				//log.infoln("Can't find pConn to received peer IP = ", node.upf.peersIP[upfIndex])
				if !sdreqMsg.reforward {
					respCh <- ie.NewCause(ie.CauseRequestRejected)
				}
				continue
			}
			pConn = v.(*PFCPConn)
		}
		//fmt.Println("parham log: selected upfIndex = ", upfIndex)

		sdreq.Header.SEID = sdreqMsg.upSeid
		//fmt.Println("parham log : send session deletion req from up to real in down")
		if sdreqMsg.reforward == true {
			sdreq.Header.MessagePriority = 123
		}
		if !sdreqMsg.reforward {
			pConn.upf.seidToRespCh[sdreqMsg.upSeid] = respCh
		}
		fmt.Println("sending ses del to Real PFCP")
		pConn.forwardToRealPFCP(sdreq, comCh)

	}
}

func (node *PFCPNode) listenForResetSes(comCh CommunicationChannel) {
	for {
		<-comCh.ResetSessions
		fmt.Println("ses rst signal received by down")
		//fmt.Println("start reseting all upfs' sessions")
		for k, v := range node.upf.lbmap {
			node.sendDeletionReq(k, v, comCh)
		}

		for i := range node.upf.peersIP {
			node.upf.upfsSessions[i] = node.upf.upfsSessions[i][:0]
		}

		for key := range node.upf.lbmap {
			delete(node.upf.lbmap, key)
		}
	}
}

func (node *PFCPNode) sendDeletionReq(sessId uint64, upfId int, comCh CommunicationChannel) {
	upfAddr := node.upf.peersIP[upfId] + ":" + DownPFCPPort
	upfpconn, ok := node.pConns.Load(upfAddr)
	if !ok {
		//fmt.Println("parham log : can not find source Pconn in node.pConns.Load(sourceAddr)")
		return
	}
	upfPconn := upfpconn.(*PFCPConn)
	sess, ok := upfPconn.sessionStore.GetSession(sessId)
	if !ok {
		//fmt.Println("parham log : can not find session = ", sessId, "in sPconn.sessionStore.GetSession(v)")
		return
	}
	delMsg := message.NewSessionDeletionRequest(0, 0, sess.localSEID, upfPconn.getSeqNum(), 123,
		nil,
	)

	sesDelMsg := SesDelU2dMsg{
		msg:       delMsg,
		upSeid:    sess.localSEID,
		reforward: true,
		upfIndex:  upfId,
		pConn:     upfPconn,
	}
	comCh.SesDelU2d <- &sesDelMsg
	upfPconn.RemoveSession(sess)
}

func (node *PFCPNode) handleNewPeers(comCh CommunicationChannel, pos Position) {
	//fmt.Println("parham log : start handleNewPeers func")
	lAddrStr := node.LocalAddr().String()
	//log.infoln("listening for new PFCP connections on", lAddrStr, "for ", pos)

	//node.tryConnectToN4Peers(lAddrStr)

	//fmt.Println("parham log : before loop for ", pos)

	if pos == Up {
		for {
			buf := make([]byte, 1024)
			//fmt.Println("parham log : buf created")
			n, rAddr, err := node.ReadFrom(buf)
			//fmt.Println("parham log : buf read")
			if err != nil {
				log.Errorln("Error while reading from conn to buf ", err)
				if errors.Is(err, net.ErrClosed) {
					return
				}

				continue
			}

			rAddrStr := rAddr.String()
			//fmt.Println("parham log : rAddr read")
			_, ok := node.pConns.Load(rAddrStr)
			if ok {
				log.Warnln("Drop packet for existing PFCPconn received from", rAddrStr)
				continue
			}
			//fmt.Println("parham log : call NewPFCPConn from handleNewPeers func")
			//if pos == Up {
			//fmt.Println("parham log : sending recieved msg to down pfcp")
			//bufTemp := buf
			comCh.U2d <- buf[:n]
			//}
			//time.Sleep(1 * time.Minute)
			//if pos == Up {
			//fmt.Println("parham log: calling NewPFCPConn for up")
			//} else {
			//	fmt.Println("parham log: calling NewPFCPConn for down")
			//}
			node.NewPFCPConn(lAddrStr, rAddrStr, buf[:n], comCh, pos)
		}
	}
	if pos == Down {
		//fmt.Println("parham log : show recieved msg from up pfcp")
		//fmt.Println(<-comCh.U2d)
	}
}

// Serve listens for the first packet from a new PFCP peer and creates PFCPConn.
func (node *PFCPNode) Serve(comCh CommunicationChannel, pos Position) {
	if pos == Up {
		//fmt.Println("parham log: calling handleNewPeers for up")
	} else {
		//fmt.Println("parham log: calling handleNewPeers for down")
	}
	go node.handleNewPeers(comCh, pos)

	shutdown := false

	for !shutdown {
		select {
		//case fseid := <-node.upf.reportNotifyChan:
		//	// TODO: Logic to distinguish PFCPConn based on SEID
		//	node.pConns.Range(func(key, value interface{}) bool {
		//		pConn := value.(*PFCPConn)
		//		pConn.handleDigestReport(fseid)
		//		return false
		//	})
		case rAddr := <-node.pConnDone:
			node.pConns.Delete(rAddr)
			//log.infoln("Removed connection to", rAddr)
		case <-node.ctx.Done():
			shutdown = true

			//log.infoln("Shutting down PFCP node")

			err := node.Close()
			if err != nil {
				log.Errorln("Error closing PFCPNode conn", err)
			}

			// Clear out the remaining pconn completions
		clearLoop:
			for {
				select {
				case rAddr, ok := <-node.pConnDone:
					{
						if !ok {
							// channel is closed, break
							break clearLoop
						}
						node.pConns.Delete(rAddr)
						//log.infoln("Removed connection to", rAddr)
					}
				default:
					// nothing to read from channel
					break clearLoop
				}
			}

			if len(node.pConnDone) > 0 {
				for rAddr := range node.pConnDone {
					node.pConns.Delete(rAddr)
					//log.infoln("Removed connection to", rAddr)
				}
			}

			close(node.pConnDone)
			//log.infoln("Done waiting for PFCPConn completions")

			node.upf.Exit()
		}
	}

	close(node.done)
}

func (node *PFCPNode) Stop() {
	node.cancel()

	//if err := node.metrics.Stop(); err != nil {
	//	// TODO: propagate error upwards
	//	log.Errorln(err)
	//}
}

// Done waits for Shutdown() to complete
func (node *PFCPNode) Done() {
	<-node.done
	//log.infoln("Shutdown complete")
}
