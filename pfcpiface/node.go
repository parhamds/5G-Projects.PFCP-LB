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

func (node *PFCPNode) tryConnectToN4Peers(lAddrStr string, comCh CommunicationChannel) {
	//fmt.Println("parham log : start tryConnectToN4Peers func")
	for _, peer := range node.upf.peersIP {
		conn, err := net.Dial("udp", peer+":"+DownPFCPPort)
		if err != nil {
			log.Warnln("Failed to establish PFCP connection to peer ", peer)
			continue
		}

		remoteAddr := conn.RemoteAddr().(*net.UDPAddr)
		n4DstIP := remoteAddr.IP

		log.WithFields(log.Fields{
			"SPGWC/SMF host": peer,
			"CP node":        n4DstIP.String(),
		}).Info("Establishing PFCP Conn with CP node")
		//fmt.Println("parham log : call NewPFCPConn from tryConnectToN4Peers func for down")
		pfcpConn := node.NewPFCPConn(lAddrStr, n4DstIP.String()+":"+DownPFCPPort, nil, comCh)
		if pfcpConn != nil {

			go pfcpConn.sendAssociationRequest()
		}
	}
}

func (node *PFCPNode) listenForSesEstReq(comCh CommunicationChannel) {
	for {
		sereq := <-comCh.SesEstU2d
		if len(node.upf.peersIP) > 0 {
			rAddr := node.upf.peersIP[0] + ":" + DownPFCPPort
			v, ok := node.pConns.Load(rAddr)
			if !ok {
				log.Infoln("Can't find pConn to received peer IP = ", node.upf.peersIP[0])
				comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
				continue
			}
			pConn := v.(*PFCPConn)
			sereq.NodeID = pConn.nodeID.localIE
			fseid, err := sereq.CPFSEID.FSEID()
			remoteSEID := fseid.SEID
			session, ok := pConn.NewPFCPSession(remoteSEID)
			if !ok {
				log.Errorf("can not create session in down: %v", err)
				comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
				continue
			}

			peerSessions := pConn.upf.peersSessions[0]
			if _, ok := peerSessions[remoteSEID]; !ok {
				peerSessions[remoteSEID] = &Sessionsinfo{}
			} else {
				log.Infoln("fseid already exists")
			}
			peerSessions[remoteSEID].LSeidDown = session.localSEID

			err = pConn.store.PutSession(session)
			if err != nil {
				log.Errorf("Failed to put PFCP session to store: %v", err)
			}
			var localFSEID *ie.IE

			localIP := pConn.LocalAddr().(*net.UDPAddr).IP
			if localIP.To4() != nil {
				localFSEID = ie.NewFSEID(session.localSEID, localIP, nil)
			} else {
				localFSEID = ie.NewFSEID(session.localSEID, nil, localIP)
			}
			sereq.CPFSEID = localFSEID

			pConn.forwardToRealPFCP(sereq, comCh)
		}
	}
}

func (node *PFCPNode) handleNewPeers(comCh CommunicationChannel, pos Position) {
	//fmt.Println("parham log : start handleNewPeers func")
	lAddrStr := node.LocalAddr().String()
	log.Infoln("listening for new PFCP connections on", lAddrStr, "for ", pos)

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
			node.NewPFCPConn(lAddrStr, rAddrStr, buf[:n], comCh)
		}
	}
	if pos == Down {
		//fmt.Println("parham log : show recieved msg from up pfcp")
		fmt.Println(<-comCh.U2d)
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
			log.Infoln("Removed connection to", rAddr)
		case <-node.ctx.Done():
			shutdown = true

			log.Infoln("Shutting down PFCP node")

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
						log.Infoln("Removed connection to", rAddr)
					}
				default:
					// nothing to read from channel
					break clearLoop
				}
			}

			if len(node.pConnDone) > 0 {
				for rAddr := range node.pConnDone {
					node.pConns.Delete(rAddr)
					log.Infoln("Removed connection to", rAddr)
				}
			}

			close(node.pConnDone)
			log.Infoln("Done waiting for PFCPConn completions")

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
	log.Infoln("Shutdown complete")
}
