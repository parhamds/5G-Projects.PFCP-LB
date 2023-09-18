// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Intel Corporation

package pfcpiface

import (
	"errors"
	"fmt"
	"net"
	"strings"

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

	sereqMsg := SesEstU2dMsg{
		msg:    sereq,
		upSeid: session.localSEID,
	}

	comCh.SesEstU2d <- &sereqMsg
	//addPDRs := make([]pdr, 0, MaxItems)
	//addFARs := make([]far, 0, MaxItems)
	//addQERs := make([]qer, 0, MaxItems)

	//for _, cPDR := range sereq.CreatePDR {
	//	var p pdr
	//	if err := p.parsePDR(cPDR, session.localSEID, pConn.appPFDs, upf.ippool); err != nil {
	//		return errProcessReply(err, ie.CauseRequestRejected)
	//	}
	//
	//	p.fseidIP = fseidIP
	//	session.CreatePDR(p)
	//	addPDRs = append(addPDRs, p)
	//}

	//for _, cFAR := range sereq.CreateFAR {
	//	var f far
	//	if err := f.parseFAR(cFAR, session.localSEID, upf, create); err != nil {
	//		return errProcessReply(err, ie.CauseRequestRejected)
	//	}
	//
	//	f.fseidIP = fseidIP
	//	session.CreateFAR(f)
	//	addFARs = append(addFARs, f)
	//}

	//for _, cQER := range sereq.CreateQER {
	//	var q qer
	//	if err := q.parseQER(cQER, session.localSEID); err != nil {
	//		return errProcessReply(err, ie.CauseRequestRejected)
	//	}
	//
	//	q.fseidIP = fseidIP
	//	session.CreateQER(q)
	//	addQERs = append(addQERs, q)
	//}

	//session.MarkSessionQer(session.qers)
	// FIXME: since PacketForwardingRules doesn't store pointers,
	//  we must also mark session QERs in addQERs.
	//  We need a kind of refactoring to clean it up.
	//session.MarkSessionQer(addQERs)
	// session.PacketForwardingRules stores all PFCP rules that has been installed so far,
	// while 'updated' stores only the PFCP rules that have been provided in this particular message.
	//updated := PacketForwardingRules{
	//	pdrs: addPDRs,
	//	fars: addFARs,
	//	qers: addQERs,
	//}

	//cause := upf.SendMsgToUPF(upfMsgTypeAdd, session.PacketForwardingRules, updated)
	//if cause == ie.CauseRequestRejected {
	//	pConn.RemoveSession(session)
	//	return errProcessReply(ErrWriteToDatapath,
	//		ie.CauseRequestRejected)
	//}

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
	respCause := <-comCh.SesEstRespCuzD2U

	//peerSessions := pConn.upf.peersSessions[0]
	//if _, ok := peerSessions[remoteSEID]; !ok {
	//	log.Errorln("peersSessions for this sesion is not created in down")
	//	return errProcessReply(ErrAllocateSession,
	//		ie.CauseNoResourcesAvailable)
	//}
	//peerSessions[remoteSEID].LSeidUp = session.localSEID

	//fmt.Println("!!!!!!!!!!!!!! parham log : peerSessions[remoteSEID] = ", peerSessions[remoteSEID])

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

	//addPdrInfo(seres, &session)

	return seres, nil
}

func (pConn *PFCPConn) handleSessionEstablishmentResponse(msg message.Message, comCh CommunicationChannel) {
	fmt.Println("parham log : handling SessionEstablishmentResponse in down")
	seres, ok := msg.(*message.SessionEstablishmentResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionEstablishmentResponse")
		comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}

	causeValue, err := seres.Cause.Cause()
	if err != nil {
		log.Errorln("can not extract response cause")
		comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}
	if causeValue != ie.CauseRequestAccepted {
		log.Errorln("session establishment not accepted by real pfcp")
		comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}

	//peerSessions := pConn.upf.peersSessions[0]
	//for k, v := range peerSessions {
	//	if v.LSeidDown == seres.Header.SEID {
	//		remoteSEID, err := seres.UPFSEID.FSEID()
	//		if err != nil {
	//			log.Errorln("error while reading fseid from real pfcp response")
	//			break
	//		}
	//		peerSessions[k].RealPFCPSeid = remoteSEID.SEID
	//		break
	//	}
	//}

	//fmt.Println("parham log : printing all localtoSMFstore ...")
	//sessions := pConn.localtoSMFstore.GetAllSessions()
	//for _, v := range sessions {
	//	fmt.Println("local = ", v.localSEID, " , smf = ", v.remoteSEID)
	//}
	//fmt.Println("parham log : printing all smftoLocalstore ...")
	//sessionss := pConn.smftoLocalstore.GetAllSessions()
	//for _, v := range sessionss {
	//	fmt.Println("local = ", v.localSEID, " , smf = ", v.remoteSEID)
	//}

	//session, ok := pConn.sessionStore.GetSession(lseid)
	//if ok == false {
	//	fmt.Println("parham log : send received msg's cause from real to up in down")
	//	log.Errorln("can not get session from pConn.localtoSMFstore, lseid = ", lseid)
	//	comCh.SesEstRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
	//	return
	//}

	//upseid := session.remoteSEID

	//fmt.Println("parham log : real seid succesfully added to SMFtoRealstore, real seid = ", realSeid.SEID, " , smf = ", smfseid)
	fmt.Println("parham log : send received msg's cause from real to up in down")
	comCh.SesEstRespCuzD2U <- seres.Cause
}

func (pConn *PFCPConn) handleSessionModificationResponse(msg message.Message, comCh CommunicationChannel) {
	fmt.Println("parham log : handling SessionModificationResponse in down")
	smres, ok := msg.(*message.SessionModificationResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionModificationResponse")
		fmt.Println("parham log : send received msg's cause from real to up in down")
		comCh.SesModRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}

	fmt.Println("parham log : send received msg's cause from real to up in down")
	comCh.SesModRespCuzD2U <- smres.Cause
}

func (pConn *PFCPConn) handleSessionModificationRequest(msg message.Message, comCh CommunicationChannel) (message.Message, error) {
	//upf := pConn.upf

	smreq, ok := msg.(*message.SessionModificationRequest)
	if !ok {
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
	smreqMsg := SesModU2dMsg{
		msg:    smreq,
		upSeid: localSEID,
	}
	comCh.SesModU2d <- &smreqMsg

	session, ok := pConn.sessionStore.GetSession(localSEID)
	if !ok {
		return sendError(ErrNotFoundWithParam("PFCP session", "localSEID", localSEID))
	}

	//var fseidIP uint32

	if smreq.CPFSEID != nil {
		fseid, err := smreq.CPFSEID.FSEID()
		if err == nil {
			session.remoteSEID = fseid.SEID
			//fseidIP = ip2int(fseid.IPv4Address)

			log.Traceln("Updated FSEID from session modification request")
		}
	}

	remoteSEID = session.remoteSEID

	//addPDRs := make([]pdr, 0, MaxItems)
	//addFARs := make([]far, 0, MaxItems)
	//addQERs := make([]qer, 0, MaxItems)
	//endMarkerList := make([][]byte, 0, MaxItems)

	//for _, cPDR := range smreq.CreatePDR {
	//	var p pdr
	//	if err := p.parsePDR(cPDR, localSEID, pConn.appPFDs, upf.ippool); err != nil {
	//		return sendError(err)
	//	}
	//
	//	p.fseidIP = fseidIP
	//
	//	session.CreatePDR(p)
	//	addPDRs = append(addPDRs, p)
	//}

	//for _, cFAR := range smreq.CreateFAR {
	//	var f far
	//	if err := f.parseFAR(cFAR, localSEID, upf, create); err != nil {
	//		return sendError(err)
	//	}
	//
	//	f.fseidIP = fseidIP
	//
	//	session.CreateFAR(f)
	//	addFARs = append(addFARs, f)
	//}

	//for _, cQER := range smreq.CreateQER {
	//	var q qer
	//	if err := q.parseQER(cQER, localSEID); err != nil {
	//		return sendError(err)
	//	}
	//
	//	q.fseidIP = fseidIP
	//
	//	session.CreateQER(q)
	//	addQERs = append(addQERs, q)
	//}

	//for _, uPDR := range smreq.UpdatePDR {
	//	var (
	//		p   pdr
	//		err error
	//	)
	//
	//	if err = p.parsePDR(uPDR, localSEID, pConn.appPFDs, upf.ippool); err != nil {
	//		return sendError(err)
	//	}
	//
	//	p.fseidIP = fseidIP
	//
	//	err = session.UpdatePDR(p)
	//	if err != nil {
	//		log.Errorln("session PDR update failed ", err)
	//		continue
	//	}
	//
	//	addPDRs = append(addPDRs, p)
	//}

	//for _, uFAR := range smreq.UpdateFAR {
	//	var (
	//		f   far
	//		err error
	//	)
	//
	//	if err = f.parseFAR(uFAR, localSEID, upf, update); err != nil {
	//		return sendError(err)
	//	}
	//
	//	f.fseidIP = fseidIP
	//
	//	err = session.UpdateFAR(&f, &endMarkerList)
	//	if err != nil {
	//		log.Errorln("session PDR update failed ", err)
	//		continue
	//	}
	//
	//	addFARs = append(addFARs, f)
	//}

	//for _, uQER := range smreq.UpdateQER {
	//	var (
	//		q   qer
	//		err error
	//	)
	//
	//	if err = q.parseQER(uQER, localSEID); err != nil {
	//		return sendError(err)
	//	}
	//
	//	q.fseidIP = fseidIP
	//
	//	err = session.UpdateQER(q)
	//	if err != nil {
	//		log.Errorln("session QER update failed ", err)
	//		continue
	//	}
	//
	//	addQERs = append(addQERs, q)
	//}

	//session.MarkSessionQer(session.qers)
	// FIXME: since PacketForwardingRules doesn't store pointers,
	//  we must also mark session QERs in addQERs.
	//  We need a kind of refactoring to clean it up.
	//session.MarkSessionQer(addQERs)

	//updated := PacketForwardingRules{
	//	pdrs: addPDRs,
	//	fars: addFARs,
	//	qers: addQERs,
	//}

	//cause := upf.SendMsgToUPF(upfMsgTypeMod, session.PacketForwardingRules, updated)
	//if cause == ie.CauseRequestRejected {
	//	return sendError(ErrWriteToDatapath)
	//}

	//if upf.EnableEndMarker {
	//	err := upf.SendEndMarkers(&endMarkerList)
	//	if err != nil {
	//		log.Errorln("Sending End Markers Failed : ", err)
	//	}
	//}

	//delPDRs := make([]pdr, 0, MaxItems)
	//delFARs := make([]far, 0, MaxItems)
	//delQERs := make([]qer, 0, MaxItems)

	//for _, rPDR := range smreq.RemovePDR {
	//	pdrID, err := rPDR.PDRID()
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	p, err := session.RemovePDR(uint32(pdrID))
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	delPDRs = append(delPDRs, *p)
	//}

	//for _, dFAR := range smreq.RemoveFAR {
	//	farID, err := dFAR.FARID()
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	f, err := session.RemoveFAR(farID)
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	delFARs = append(delFARs, *f)
	//}

	//for _, dQER := range smreq.RemoveQER {
	//	qerID, err := dQER.QERID()
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	q, err := session.RemoveQER(qerID)
	//	if err != nil {
	//		return sendError(err)
	//	}
	//
	//	delQERs = append(delQERs, *q)
	//}

	//deleted := PacketForwardingRules{
	//	pdrs: delPDRs,
	//	fars: delFARs,
	//	qers: delQERs,
	//}

	//cause = upf.SendMsgToUPF(upfMsgTypeDel, deleted, PacketForwardingRules{})
	//if cause == ie.CauseRequestRejected {
	//	return sendError(ErrWriteToDatapath)
	//}

	err := pConn.sessionStore.PutSession(session)
	if err != nil {
		log.Errorf("Failed to put PFCP session to store: %v", err)
	}
	respCause := <-comCh.SesModRespCuzD2U

	causeValue, err := respCause.Cause()
	if err != nil {
		log.Errorln("can not extract response cause")
	}
	if causeValue != ie.CauseRequestAccepted {
		return sendError(ErrNotFoundWithParam("reject from real pfcp", "localSEID", localSEID))
	}

	// Build response message
	smres := message.NewSessionModificationResponse(0, /* MO?? <-- what's this */
		0,                                    /* FO <-- what's this? */
		remoteSEID,                           /* seid */
		smreq.SequenceNumber,                 /* seq # */
		0,                                    /* priority */
		ie.NewCause(ie.CauseRequestAccepted), /* accept it blindly for the time being */
	)

	return smres, nil
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
	sdreqMsg := SesDelU2dMsg{
		msg:    sdreq,
		upSeid: localSEID,
	}
	comCh.SesDelU2d <- &sdreqMsg
	session, ok := pConn.sessionStore.GetSession(localSEID)
	if !ok {
		return sendError(ErrNotFoundWithParam("PFCP session", "localSEID", localSEID))
	}

	//cause := upf.SendMsgToUPF(upfMsgTypeDel, session.PacketForwardingRules, PacketForwardingRules{})
	//if cause == ie.CauseRequestRejected {
	//	return sendError(ErrWriteToDatapath)
	//}

	//if err := releaseAllocatedIPs(upf.ippool, &session); err != nil {
	//	return sendError(ErrOperationFailedWithReason("session IP dealloc", err.Error()))
	//}

	/* delete sessionRecord */
	respCause := <-comCh.SesDelRespCuzD2U
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
}

func (pConn *PFCPConn) handleSessionDeletionResponse(msg message.Message, comCh CommunicationChannel) {
	fmt.Println("parham log : handling SessionDeletionnResponse in down")
	sdres, ok := msg.(*message.SessionDeletionResponse)
	if !ok {
		log.Errorln("can not convert recieved msg to SessionDeletionResponse")
		fmt.Println("parham log : send received msg's cause from real to up in down")
		comCh.SesDelRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}
	causeValue, err := sdres.Cause.Cause()
	if err != nil {
		log.Errorln("can not extract response cause")
		comCh.SesDelRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}
	if causeValue != ie.CauseRequestAccepted {
		log.Errorln("session deletion not accepted by real pfcp")
		comCh.SesDelRespCuzD2U <- ie.NewCause(ie.CauseRequestRejected)
		return
	}

	downseid := sdres.Header.SEID
	session, ok := pConn.sessionStore.GetSession(downseid)

	pConn.RemoveSession(session)

	fmt.Println("parham log : send received msg's cause from real to up in down")
	comCh.SesDelRespCuzD2U <- ie.NewCause(ie.CauseRequestAccepted)
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
