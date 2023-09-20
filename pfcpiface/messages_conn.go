// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Intel Corporation

package pfcpiface

import (
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

var errFlowDescAbsent = errors.New("flow description not present")
var errDatapathDown = errors.New("datapath down")
var errReqRejected = errors.New("request rejected")

func (pConn *PFCPConn) sendAssociationRequest(pfcpInfo PfcpInfo, comCh CommunicationChannel) {
	// Build request message
	asreq := message.NewAssociationSetupRequest(pConn.getSeqNum(),
		pConn.associationIEs()...,
	)

	r := newRequest(asreq)
	reply, timeout := pConn.sendPFCPRequestMessage(r)

	if reply != nil {
		err := pConn.handleAssociationSetupResponse(reply, pfcpInfo, comCh)
		if err != nil {
			log.Errorln("Handling of Assoc Setup Response Failed ", pConn.RemoteAddr())
			//fmt.Println("parham log : Shutdown called from sendAssociationRequest")
			pConn.Shutdown()

			return
		}

		//fmt.Println("parham log : pConn.upf.enableHBTimer = ", pConn.upf.enableHBTimer)
		if pConn.upf.enableHBTimer || true {
			//fmt.Println("parham log : starting pConn.startHeartBeatMonitor()")
			go pConn.startHeartBeatMonitor()
		}
	} else if timeout {
		//fmt.Println("parham log : Shutdown called from sendAssociationRequest, timeout channel")
		pConn.Shutdown()
	}
}

func (pConn *PFCPConn) forwardToRealPFCP(msg message.Message, comCh CommunicationChannel) {
	// Build request message
	fmt.Println("parham log : sending a message to Real PFCP")

	r := newRequest(msg)
	//_, _ = pConn.sendPFCPRequestMessage(r)
	pConn.forwardPFCPRequestMessage(r)
	//fmt.Println("parham log : response received from Real PFCP")
	//if reply != nil {
	//	pConn.HandleForwardedMsgResp(reply, comCh)
	//} else if timeout {
	//	log.Warn("Timeout for forwarded message")
	//} !!!!!!!!!!!!!! it will be read by handlepfcpmsg func
}

//func (pConn *PFCPConn) HandleForwardedMsgResp(msg message.Message, comCh CommunicationChannel,node ) {
//	switch msg.MessageType() {
//	case message.MsgTypeSessionEstablishmentResponse:
//		pConn.handleSessionEstablishmentResponse(msg, comCh,node)
//	}
//}

//func (pConn *PFCPConn) ForwardAssociationRequest(msg message.Message, comCh CommunicationChannel) {
//
//	r := newRequest(msg)
//	//fmt.Println("parham log : sending msg to real pfcp")
//	reply, timeout := pConn.sendPFCPRequestMessage(r)
//	//fmt.Println("parham log : recievd msg from real pfcp")
//	if reply != nil {
//		//fmt.Println("parham log : sending msg to up")
//		comCh.D2u <- reply
//	} else if timeout {
//		//fmt.Println("parham log : Shutdown called from sendAssociationRequest, timeout channel")
//		pConn.Shutdown()
//	}
//}

func (pConn *PFCPConn) getHeartBeatRequest() *Request {
	seq := pConn.getSeqNum()

	hbreq := message.NewHeartbeatRequest(
		seq,
		ie.NewRecoveryTimeStamp(pConn.ts.local),
		nil,
	)

	return newRequest(hbreq)
}

func (pConn *PFCPConn) handleHeartbeatRequest(msg message.Message) (message.Message, error) {
	hbreq, ok := msg.(*message.HeartbeatRequest)
	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	if pConn.upf.enableHBTimer {
		// reset heartbeat expiry timer
		// non-blocking write to channel
		select {
		case pConn.hbReset <- struct{}{}:
			// timer reset
		default:
			// channel full, log warning and ignore
			log.Warn("failed to reset heartbeat timer")
		}
	}

	// TODO: Check and update remote recovery timestamp

	// Build response message
	hbres := message.NewHeartbeatResponse(hbreq.SequenceNumber,
		ie.NewRecoveryTimeStamp(pConn.ts.local), /* ts */
	)

	return hbres, nil
}

func (pConn *PFCPConn) handleIncomingResponse(msg message.Message) {
	req, ok := pConn.pendingReqs.Load(msg.Sequence())

	if ok {
		req.(*Request).reply <- msg
		pConn.pendingReqs.Delete(msg.Sequence())
	}
}

func (pConn *PFCPConn) associationIEs() []*ie.IE {
	upf := pConn.upf
	networkInstance := string(ie.NewNetworkInstanceFQDN(upf.Dnn).Payload)
	//fmt.Println("parham log : networkInstance = ", networkInstance)
	flags := uint8(0x41)
	//fmt.Println("parham log : flags = ", flags)

	if len(upf.Dnn) != 0 {
		log.Infoln("Association Setup with DNN:", upf.Dnn)
		//fmt.Println("parham log : upf.dnn = ", upf.Dnn)
		// add ASSONI flag to set network instance.
		flags = uint8(0x61)
		//fmt.Println("parham log : flags = ", flags)
	}

	features := make([]uint8, 4)

	if upf.EnableUeIPAlloc {
		setUeipFeature(features...)
		//fmt.Println("parham log : upf.enableUeIPAlloc is enable and features = ", features)
	}

	if upf.EnableEndMarker {
		setEndMarkerFeature(features...)
		//fmt.Println("parham log : upf.enableUeIPAlloc is enable and features = ", features)
	}
	//fmt.Println("parham log : upf.accessIP = ", upf.AccessIP)
	//fmt.Println("parham log : upf.coreIP = ", upf.CoreIP)
	ies := []*ie.IE{
		ie.NewRecoveryTimeStamp(pConn.ts.local),
		pConn.nodeID.localIE,
		// 0x41 = Spare (0) | Assoc Src Inst (1) | Assoc Net Inst (0) | Tied Range (000) | IPV6 (0) | IPV4 (1)
		//      = 01000001
		ie.NewUserPlaneIPResourceInformation(flags, 0, upf.AccessIP.String(), "", networkInstance, ie.SrcInterfaceAccess),
		// ie.NewUserPlaneIPResourceInformation(0x41, 0, coreIP, "", "", ie.SrcInterfaceCore),
		ie.NewUPFunctionFeatures(features...),
	}

	return ies
}

func (pConn *PFCPConn) lbAssociationIEs(upf *Upf) []*ie.IE {
	//upf := pConn.upf
	networkInstance := string(ie.NewNetworkInstanceFQDN(upf.Dnn).Payload)
	//fmt.Println("parham log : networkInstance = ", networkInstance)
	flags := uint8(0x41)
	//fmt.Println("parham log : flags = ", flags)

	if len(upf.Dnn) != 0 {
		log.Infoln("Association Setup with DNN:", upf.Dnn)
		//fmt.Println("parham log : upf.dnn = ", upf.Dnn)
		// add ASSONI flag to set network instance.
		flags = uint8(0x61)
		//fmt.Println("parham log : flags = ", flags)
	}

	features := make([]uint8, 4)

	if upf.EnableUeIPAlloc {
		setUeipFeature(features...)
		//fmt.Println("parham log : upf.enableUeIPAlloc is enable and features = ", features)
	}

	if upf.EnableEndMarker {
		setEndMarkerFeature(features...)
		//fmt.Println("parham log : upf.enableUeIPAlloc is enable and features = ", features)
	}
	//fmt.Println("parham log : upf.accessIP = ", upf.AccessIP)
	//fmt.Println("parham log : upf.coreIP = ", upf.CoreIP)
	ies := []*ie.IE{
		ie.NewRecoveryTimeStamp(pConn.ts.local),
		//ie.NewNodeID(upf.NodeID, "", ""),
		pConn.nodeID.localIE,
		// 0x41 = Spare (0) | Assoc Src Inst (1) | Assoc Net Inst (0) | Tied Range (000) | IPV6 (0) | IPV4 (1)
		//      = 01000001

		ie.NewUserPlaneIPResourceInformation(flags, 0, upf.AccessIP.String(), "", networkInstance, ie.SrcInterfaceAccess),
		// ie.NewUserPlaneIPResourceInformation(0x41, 0, coreIP, "", "", ie.SrcInterfaceCore),
		ie.NewUPFunctionFeatures(features...),
	}

	return ies
}

func (pConn *PFCPConn) handleAssociationSetupRequest(msg message.Message, comCh CommunicationChannel) (message.Message, error) {
	//fmt.Println("!!!!! parham log : start handleAssociationSetupRequest !!!!!")
	addr := pConn.RemoteAddr().String()
	//fmt.Println("parham log : remote addr = ", addr)
	//upf := pConn.upf

	asreq, ok := msg.(*message.AssociationSetupRequest)

	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	nodeID, err := asreq.NodeID.NodeID()
	//fmt.Println("parham log : nodeID = ", nodeID)
	if err != nil {
		return nil, errUnmarshal(err)
	}

	ts, err := asreq.RecoveryTimeStamp.RecoveryTimeStamp()
	//fmt.Println("parham log : ts = ", ts)
	if err != nil {
		return nil, errUnmarshal(err)
	}
	//fmt.Println("parham log : asreq.SequenceNumber = ", asreq.SequenceNumber)
	// Build response message
	if len(pConn.upf.peersUPF) == 0 {
		return nil, errors.New("there is no real upf there yet ...")
	}
	realUPF := pConn.upf.peersUPF[0]
	asres := message.NewAssociationSetupResponse(asreq.SequenceNumber,
		pConn.lbAssociationIEs(realUPF)...)

	//if !upf.isConnected() {
	//	asres.Cause = ie.NewCause(ie.CauseRequestRejected)
	//	return asres, errProcess(errDatapathDown)
	//}

	if pConn.ts.remote.IsZero() {
		pConn.ts.remote = ts
		log.Infoln("Association Setup Request from", addr,
			"with recovery timestamp:", ts)
	} else if ts.After(pConn.ts.remote) {
		old := pConn.ts.remote
		pConn.ts.remote = ts
		log.Warnln("Association Setup Request from", addr,
			"with newer recovery timestamp:", ts, "older:", old)
	}

	pConn.nodeID.remote = nodeID
	asres.Cause = ie.NewCause(ie.CauseRequestAccepted)

	log.Infoln("Association setup done between nodes",
		"local:", pConn.nodeID.local, "remote:", pConn.nodeID.remote)

	return asres, nil
}

func (pConn *PFCPConn) handleAssociationSetupResponse(msg message.Message, pfcpInfo PfcpInfo, comCh CommunicationChannel) error {
	addr := pConn.RemoteAddr().String()

	asres, ok := msg.(*message.AssociationSetupResponse)
	if !ok {
		return errUnmarshal(errMsgUnexpectedType)
	}

	cause, err := asres.Cause.Cause()
	if err != nil {
		return errUnmarshal(err)
	}

	if cause != ie.CauseRequestAccepted {
		log.Errorln("Association Setup Response from", addr,
			"with Cause:", cause)
		return errReqRejected
	}

	nodeID, err := asres.NodeID.NodeID()
	if err != nil {
		return errUnmarshal(err)
	}

	ts, err := asres.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		return errUnmarshal(err)
	}

	if pConn.ts.remote.IsZero() {
		pConn.ts.remote = ts
		log.Infoln("Association Setup Response from", addr,
			"with recovery timestamp:", ts)
	} else if ts.After(pConn.ts.remote) {
		old := pConn.ts.remote
		pConn.ts.remote = ts
		log.Warnln("Association Setup Response from", addr,
			"with newer recovery timestamp:", ts, "older:", old)
	}

	pConn.nodeID.remote = nodeID
	log.Infoln("Association setup done between nodes",
		"local:", pConn.nodeID.local, "remote:", pConn.nodeID.remote)
	comCh.UpfD2u <- &pfcpInfo
	return nil
}

func (pConn *PFCPConn) handleAssociationReleaseRequest(msg message.Message) (message.Message, error) {
	arreq, ok := msg.(*message.AssociationReleaseRequest)
	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	// Build response message
	arres := message.NewAssociationReleaseResponse(arreq.SequenceNumber,
		ie.NewRecoveryTimeStamp(pConn.ts.local),
		pConn.nodeID.localIE,
		ie.NewCause(ie.CauseRequestAccepted),
	)

	return arres, nil
}

func (pConn *PFCPConn) handlePFDMgmtRequest(msg message.Message) (message.Message, error) {
	pfdmreq, ok := msg.(*message.PFDManagementRequest)
	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	currentAppPFDs := pConn.appPFDs

	// On every PFD management request reset existing contents
	// TODO: Analyse impact on PDRs referencing these IDs
	pConn.ResetAppPFDs()

	errUnmarshalReply := func(err error, offendingIE *ie.IE) (message.Message, error) {
		// Revert the map to original contents
		pConn.appPFDs = currentAppPFDs
		// Build response message
		pfdres := message.NewPFDManagementResponse(pfdmreq.SequenceNumber,
			ie.NewCause(ie.CauseRequestRejected),
			offendingIE,
		)

		return pfdres, errUnmarshal(err)
	}

	for _, appIDPFD := range pfdmreq.ApplicationIDsPFDs {
		id, err := appIDPFD.ApplicationID()
		if err != nil {
			return errUnmarshalReply(err, appIDPFD)
		}

		pConn.NewAppPFD(id)
		appPFD := pConn.appPFDs[id]

		pfdCtx, err := appIDPFD.PFDContext()
		if err != nil {
			pConn.RemoveAppPFD(id)
			return errUnmarshalReply(err, appIDPFD)
		}

		for _, pfdContent := range pfdCtx {
			fields, err := pfdContent.PFDContents()
			if err != nil {
				pConn.RemoveAppPFD(id)
				return errUnmarshalReply(err, appIDPFD)
			}

			if fields.FlowDescription == "" {
				return errUnmarshalReply(errFlowDescAbsent, appIDPFD)
			}

			appPFD.flowDescs = append(appPFD.flowDescs, fields.FlowDescription)
		}

		pConn.appPFDs[id] = appPFD
		//log.traceln("Flow descriptions for AppID", id, ":", appPFD.flowDescs)
	}

	// Build response message
	pfdres := message.NewPFDManagementResponse(pfdmreq.SequenceNumber,
		ie.NewCause(ie.CauseRequestAccepted),
		nil,
	)

	return pfdres, nil
}
