// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Intel Corporation

package pfcpiface

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// errors
var (
	ErrWriteToDatapath = errors.New("write to datapath failed")
	ErrAssocNotFound   = errors.New("no association found for NodeID")
	ErrAllocateSession = errors.New("unable to allocate new PFCP session")
)

func (pConn *PFCPConn) handleSessionEstablishmentRequest(msg message.Message, comCh CommunicationChannel) (message.Message, error) {
	//upf := pConn.upf

	sereq, ok := msg.(*message.SessionEstablishmentRequest)
	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	errUnmarshalReply := func(err error, offendingIE *ie.IE) (message.Message, error) {
		// Build response message
		pfdres := message.NewSessionEstablishmentResponse(0,
			0,
			0,
			sereq.SequenceNumber,
			0,
			ie.NewCause(ie.CauseRequestRejected),
			offendingIE,
		)

		return pfdres, errUnmarshal(err)
	}

	nodeID, err := sereq.NodeID.NodeID()
	if err != nil {
		return errUnmarshalReply(err, sereq.NodeID)
	}

	/* Read fseid from the IE */
	fseid, err := sereq.CPFSEID.FSEID()
	if err != nil {
		return errUnmarshalReply(err, sereq.CPFSEID)
	}
	//fmt.Println("parham log test printing nodeID : ", nodeID)
	//fmt.Println("parham log test printing fseid : ", fseid)
	remoteSEID := fseid.SEID
	//fseidIP := ip2int(fseid.IPv4Address)

	errProcessReply := func(err error, cause uint8) (message.Message, error) {
		// Build response message
		seres := message.NewSessionEstablishmentResponse(0, /* MO?? <-- what's this */
			0,                    /* FO <-- what's this? */
			remoteSEID,           /* seid */
			sereq.SequenceNumber, /* seq # */
			0,                    /* priority */
			pConn.nodeID.localIE,
			ie.NewCause(cause),
		)

		return seres, errProcess(err)
	}

	if strings.Compare(nodeID, pConn.nodeID.remote) != 0 {
		log.Warnln("Association not found for Establishment request",
			"with nodeID: ", nodeID, ", Association NodeID: ", pConn.nodeID.remote)
		return errProcessReply(ErrAssocNotFound, ie.CauseNoEstablishedPFCPAssociation)
	}

	session, ok := pConn.NewPFCPSession(remoteSEID)
	if !ok {
		return errProcessReply(ErrAllocateSession,
			ie.CauseNoResourcesAvailable)
	}
	respch := make(chan *ie.IE, 10)
	sereqMsg := SesEstU2dMsg{
		msg:    sereq,
		upSeid: session.localSEID,
		respCh: respch,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer close(respch)
	comCh.SesEstU2d <- &sereqMsg
	err = pConn.sessionStore.PutSession(session)
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

	// Build response message
	select {
	case respCause := <-respch:

		causeValue, err := respCause.Cause()
		if err != nil {
			log.Errorln("can not extract response cause")
		}
		if causeValue != ie.CauseRequestAccepted {
			return errProcessReply(ErrAllocateSession,
				ie.CauseNoResourcesAvailable)
		}
		seres := message.NewSessionEstablishmentResponse(0, /* MO?? <-- what's this */
			0,                                    /* FO <-- what's this? */
			session.remoteSEID,                   /* seid */
			sereq.SequenceNumber,                 /* seq # */
			0,                                    /* priority */
			pConn.nodeID.localIE,                 /* node id */
			ie.NewCause(ie.CauseRequestAccepted), /* accept it blindly for the time being */
			localFSEID,
		)
		return seres, nil
	case <-ctx.Done():
		//fmt.Println("timed out waiting for response from Down.")
		return errProcessReply(ErrAllocateSession,
			ie.CauseNoResourcesAvailable)
	}

}

func sendResptoUp(resp *ie.IE, respCh chan *ie.IE, reforward bool) {
	if !reforward {
		respCh <- resp
	}
}

func (pConn *PFCPConn) handleSessionEstablishmentResponse(msg message.Message, comCh CommunicationChannel, node *PFCPNode) {
	//fmt.Println("parham log : handling SessionEstablishmentResponse in down")
	seres, ok := msg.(*message.SessionEstablishmentResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionEstablishmentResponse")
		return
	}

	var respCh chan *ie.IE
	reforward := true
	if seres.Header.MessagePriority != 123 {
		respCh = pConn.upf.seidToRespCh[seres.SEID()]
		delete(pConn.upf.seidToRespCh, seres.SEID())
		reforward = false
	}

	causeValue, err := seres.Cause.Cause()
	if err != nil {
		log.Errorln("can not extract response cause")
		sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
		return
	}
	if causeValue != ie.CauseRequestAccepted {
		log.Errorln("session establishment not accepted by real pfcp")
		sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
		return
	}

	//fmt.Println("parham log : real seid succesfully added to SMFtoRealstore, real seid = ", realSeid.SEID, " , smf = ", smfseid)
	//c, err := seres.Cause.Cause()
	//fmt.Println("parham log : send received msg's cause from real to up in down : ", c)
	sendResptoUp(seres.Cause, respCh, reforward)
	if reforward {
		ModMsg, ok := node.upf.sesModMsgStore[seres.SEID()]
		if ok {
			sesModMsg := SesModU2dMsg{
				msg:       ModMsg,
				upSeid:    seres.SEID(),
				reforward: true,
			}
			comCh.SesModU2d <- &sesModMsg
		}
	}
}

func (pConn *PFCPConn) handleSessionModificationResponse(msg message.Message, comCh CommunicationChannel) {
	//fmt.Println("parham log : handling SessionModificationResponse in down")
	smres, ok := msg.(*message.SessionModificationResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionModificationResponse")
		//fmt.Println("parham log : send received msg's cause from real to up in down : ", ie.CauseRequestRejected)
		return
	}
	var respCh chan *ie.IE
	reforward := true
	if smres.Header.MessagePriority != 123 {
		respCh = pConn.upf.seidToRespCh[smres.SEID()]
		delete(pConn.upf.seidToRespCh, smres.SEID())
		reforward = false
	}

	//c, _ := smres.Cause.Cause()
	//fmt.Println("parham log : send received msg's cause from real to up in down : ", c)
	sendResptoUp(smres.Cause, respCh, reforward)
}

func (pConn *PFCPConn) handleSessionModificationRequest(msg message.Message, comCh CommunicationChannel) (message.Message, error) {
	//upf := pConn.upf
	log.Traceln("------------------ start handling ses mod req ------------------")
	startTime := time.Now()
	smreq, ok := msg.(*message.SessionModificationRequest)
	if !ok {
		log.Traceln("error while converting msg to SessionModificationRequest")
		return nil, errUnmarshal(errMsgUnexpectedType)
	}
	var remoteSEID uint64

	sendError := func(err error) (message.Message, error) {
		log.Errorln(err)

		smres := message.NewSessionModificationResponse(0, /* MO?? <-- what's this */
			0,                                    /* FO <-- what's this? */
			remoteSEID,                           /* seid */
			smreq.SequenceNumber,                 /* seq # */
			0,                                    /* priority */
			ie.NewCause(ie.CauseRequestRejected), /* accept it blindly for the time being */
		)

		return smres, err
	}

	localSEID := smreq.SEID()
	log.Traceln("localSEID = ", localSEID)
	respch := make(chan *ie.IE, 10)
	smreqMsg := SesModU2dMsg{
		msg:    smreq,
		upSeid: localSEID,
		respCh: respch,
	}
	comCh.SesModU2d <- &smreqMsg
	log.Traceln("ses est sent to down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer close(respch)
	log.Traceln("recovering session")
	session, ok := pConn.sessionStore.GetSession(localSEID)
	if !ok {
		log.Traceln("error while recovering session")
		return sendError(ErrNotFoundWithParam("PFCP session", "localSEID", localSEID))
	}

	//var fseidIP uint32

	if smreq.CPFSEID != nil {
		log.Traceln("smreq.CPFSEID != nil")
		fseid, err := smreq.CPFSEID.FSEID()
		log.Traceln("fseid = ", fseid)
		if err == nil {
			log.Traceln("old session.remoteSEID = ", session.remoteSEID)
			session.remoteSEID = fseid.SEID
			log.Traceln("updated session.remoteSEID = ", session.remoteSEID)
		} else {
			log.Traceln("error while getting fseid ", fseid)
		}
	}

	remoteSEID = session.remoteSEID
	log.Traceln("remoteSEID = ", remoteSEID)

	err := pConn.sessionStore.PutSession(session)
	log.Traceln("session put in sessionStore")
	if err != nil {
		log.Errorf("Failed to put PFCP session to store: %v", err)
		log.Traceln("error while putting session in sessionStore")
	}
	select {
	case respCause := <-respch:
		log.Traceln("resp recieved from down")
		causeValue, err := respCause.Cause()

		if err != nil {
			log.Traceln("error while getting resp cause")
		}
		log.Traceln("resp cause = ", causeValue)
		if causeValue != ie.CauseRequestAccepted {
			log.Traceln("causeValue != ie.CauseRequestAccepted")
			return sendError(ErrNotFoundWithParam("reject from real pfcp", "localSEID", localSEID))
		}

		// Build response message
		log.Traceln("building resp msg")
		smres := message.NewSessionModificationResponse(0, /* MO?? <-- what's this */
			0,                                    /* FO <-- what's this? */
			remoteSEID,                           /* seid */
			smreq.SequenceNumber,                 /* seq # */
			0,                                    /* priority */
			ie.NewCause(ie.CauseRequestAccepted), /* accept it blindly for the time being */
		)
		log.Traceln("smreq.SequenceNumber = ", smreq.SequenceNumber)
		endTime := time.Now()
		elapsedTime := endTime.Sub(startTime).Milliseconds()
		log.Traceln("total process time = ", elapsedTime, " ms")
		log.Traceln("------------------ end handling ses mod req ------------------")
		return smres, nil
	case <-ctx.Done():
		//fmt.Println("timed out waiting for response from Down.")
		return sendError(ErrNotFoundWithParam("reject from real pfcp", "localSEID", localSEID))
	}
}

func (pConn *PFCPConn) handleSessionDeletionRequest(msg message.Message, comCh CommunicationChannel) (message.Message, error) {
	//upf := pConn.upf

	sdreq, ok := msg.(*message.SessionDeletionRequest)
	if !ok {
		return nil, errUnmarshal(errMsgUnexpectedType)
	}

	sendError := func(err error) (message.Message, error) {
		smres := message.NewSessionDeletionResponse(0, /* MO?? <-- what's this */
			0,                                    /* FO <-- what's this? */
			0,                                    /* seid */
			sdreq.SequenceNumber,                 /* seq # */
			0,                                    /* priority */
			ie.NewCause(ie.CauseRequestRejected), /* accept it blindly for the time being */
		)

		return smres, err
	}

	/* retrieve sessionRecord */
	localSEID := sdreq.SEID()
	respch := make(chan *ie.IE, 10)
	sdreqMsg := SesDelU2dMsg{
		msg:    sdreq,
		upSeid: localSEID,
		respCh: respch,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer close(respch)
	comCh.SesDelU2d <- &sdreqMsg
	session, ok := pConn.sessionStore.GetSession(localSEID)
	if !ok {
		return sendError(ErrNotFoundWithParam("PFCP session", "localSEID", localSEID))
	}

	/* delete sessionRecord */
	select {
	case respCause := <-respch:

		causeValue, err := respCause.Cause()
		if err != nil {
			log.Errorln("can not extract response cause")
		}
		if causeValue != ie.CauseRequestAccepted {
			return sendError(ErrNotFoundWithParam("session deletion reject recieved from real pfcp", "localSEID", localSEID))
		}

		pConn.RemoveSession(session)

		// Build response message
		smres := message.NewSessionDeletionResponse(0, /* MO?? <-- what's this */
			0,                                    /* FO <-- what's this? */
			session.remoteSEID,                   /* seid */
			sdreq.SequenceNumber,                 /* seq # */
			0,                                    /* priority */
			ie.NewCause(ie.CauseRequestAccepted), /* accept it blindly for the time being */
		)

		return smres, nil
	case <-ctx.Done():
		//fmt.Println("timed out waiting for response from Down.")
		return sendError(ErrNotFoundWithParam("session deletion timeout from real pfcp", "localSEID", localSEID))
	}
}

func (pConn *PFCPConn) handleSessionDeletionResponse(msg message.Message, comCh CommunicationChannel, node *PFCPNode) {
	//fmt.Println("parham log : handling SessionDeletionnResponse in down")
	sdres, ok := msg.(*message.SessionDeletionResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionDeletionResponse")
		//fmt.Println("parham log : send received msg's cause from real to up in down", ie.CauseRequestRejected)
		return
	}
	var respCh chan *ie.IE
	reforward := true
	if sdres.Header.MessagePriority != 123 {
		respCh = pConn.upf.seidToRespCh[sdres.SEID()]
		delete(pConn.upf.seidToRespCh, sdres.SEID())
		reforward = false
	}

	causeValue, err := sdres.Cause.Cause()
	if err != nil {
		log.Errorln("can not extract response cause")
		//fmt.Println("parham log : send received msg's cause from real to up in down for seid = ", sdres.SEID(), " resp cause = ", ie.CauseRequestRejected)
		sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
		return
	}
	if causeValue != ie.CauseRequestAccepted {
		log.Errorln("session deletion not accepted by real pfcp")
		//fmt.Println("parham log : send received msg's cause from real to up in down for seid = ", sdres.SEID(), " resp cause = ", ie.CauseRequestRejected)
		sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
		return
	}

	if sdres.Header.MessagePriority != 123 {
		downseid := sdres.Header.SEID
		session, ok := pConn.sessionStore.GetSession(downseid)
		if !ok {
			log.Errorln(errors.New("can not find seid in pConn.sessionStore.GetSession(downseid) for seid = "), downseid)
			sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
			return
		}
		pConn.RemoveSession(session)
		err = pConn.pruneSession(node, downseid)
		if err != nil {
			log.Errorln(err)
			sendResptoUp(ie.NewCause(ie.CauseRequestRejected), respCh, reforward)
			return
		}
	}
	//fmt.Println("parham log : send received msg's cause from real to up in down for seid = ", sdres.SEID(), " resp cause = ", ie.CauseRequestAccepted)
	sendResptoUp(sdres.Cause, respCh, reforward)
}

func (pConn *PFCPConn) pruneSession(node *PFCPNode, seid uint64) error {
	//fmt.Println("parham log : start deleting session from everywhere")
	delete(node.upf.lbmap, seid)
	var found bool
	var upfIndex int
	for i, u := range node.upf.peersUPF {
		if u.NodeID == pConn.nodeID.remote {
			upfIndex = i
			found = true
			break
		}
	}
	if !found {
		return errors.New("can not find upfIndex in node.upf.peersUPF")
	}
	found = false
	var sessionIndex int
	for i, s := range node.upf.upfsSessions[upfIndex] {
		if s == seid {
			sessionIndex = i
			found = true
			break
		}
	}
	if !found {
		return errors.New("can not find sessionIndex in node.upf.upfsSessions")
	}
	node.upf.upfsSessions[upfIndex] = append(node.upf.upfsSessions[upfIndex][:sessionIndex], node.upf.upfsSessions[upfIndex][sessionIndex+1:]...)
	delete(node.upf.sesEstMsgStore, seid)
	delete(node.upf.sesModMsgStore, seid)
	//fmt.Println("parham log : done deleting session from everywhere")
	return nil
}

func (pConn *PFCPConn) handleDigestReport(fseid uint64) {
	session, ok := pConn.sessionStore.GetSession(fseid)
	if !ok {
		log.Warnln("No session found for fseid : ", fseid)
		return
	}

	seq := pConn.getSeqNum()
	srreq := message.NewSessionReportRequest(0, /* MO?? <-- what's this */
		0,                            /* FO <-- what's this? */
		0,                            /* seid */
		seq,                          /* seq # */
		0,                            /* priority */
		ie.NewReportType(0, 0, 0, 1), /*upir, erir, usar, dldr int*/
	)
	srreq.Header.SEID = session.remoteSEID

	var pdrID uint32

	var farID uint32

	for _, pdr := range session.pdrs {
		if pdr.srcIface == core {
			pdrID = pdr.pdrID

			farID = pdr.farID

			break
		}
	}

	for _, far := range session.fars {
		if far.farID == farID {
			if far.applyAction&ActionNotify == 0 {
				log.Errorln("packet received for forwarding far. discard")
				return
			}
		}
	}

	if pdrID == 0 {
		log.Errorln("No Pdr found for downlink")

		return
	}

	srreq.DownlinkDataReport = ie.NewDownlinkDataReport(
		ie.NewPDRID(uint16(pdrID)))

	log.WithFields(log.Fields{
		"F-SEID": fseid,
		"PDR ID": pdrID,
	}).Debug("Sending Downlink Data Report")

	pConn.SendPFCPMsg(srreq)
}

func (pConn *PFCPConn) handleSessionReportResponse(msg message.Message) error {
	upf := pConn.upf

	srres, ok := msg.(*message.SessionReportResponse)
	if !ok {
		return errUnmarshal(errMsgUnexpectedType)
	}

	cause := srres.Cause.Payload[0]
	if cause == ie.CauseRequestAccepted {
		return nil
	}

	log.Warnln("session req not accepted seq : ", srres.SequenceNumber)

	seid := srres.SEID()

	if cause == ie.CauseSessionContextNotFound {
		sessItem, ok := pConn.sessionStore.GetSession(seid)
		if !ok {
			return errProcess(ErrNotFoundWithParam("PFCP session context", "SEID", seid))
		}

		log.Warnln("context not found, deleting session locally")

		pConn.RemoveSession(sessItem)

		cause := upf.SendMsgToUPF(
			upfMsgTypeDel, sessItem.PacketForwardingRules, PacketForwardingRules{})
		if cause == ie.CauseRequestRejected {
			return errProcess(
				ErrOperationFailedWithParam("delete session from datapath", "seid", seid))
		}

		return nil
	}

	return nil
}
