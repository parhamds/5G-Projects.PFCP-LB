// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Intel Corporation

package pfcpiface

import (
	"errors"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/omec-project/upf-epc/pfcpiface/metrics"
)

var errMsgUnexpectedType = errors.New("unable to parse message as type specified")

type HandlePFCPMsgError struct {
	Op  string
	Err error
}

func (e *HandlePFCPMsgError) Error() string {
	return "Error during " + e.Op + ": " + e.Err.Error()
}

func errUnmarshal(err error) *HandlePFCPMsgError {
	return &HandlePFCPMsgError{Op: "Unmarshal", Err: err}
}

func errProcess(err error) *HandlePFCPMsgError {
	return &HandlePFCPMsgError{Op: "Process", Err: err}
}

type Request struct {
	msg   message.Message      // Request message
	reply chan message.Message // Response message
}

func newRequest(msg message.Message) *Request {
	return &Request{msg: msg, reply: make(chan message.Message)}
}

func (r *Request) GetResponse(done <-chan struct{}, respDuration time.Duration) (message.Message, bool) {
	respTimer := time.NewTimer(respDuration)
	select {
	case <-done:
		return nil, false
	case c := <-r.reply:
		respTimer.Stop()
		return c, false
	case <-respTimer.C:
		return nil, true
	}
}

//func (pConn *PFCPConn) SimpleForwarder(buf []byte, comCh CommunicationChannel) {
//	fmt.Println("parham log : Simple Forwader is called")
//	msg, err := message.Parse(buf)
//	if err != nil {
//		log.Errorln("Ignoring undecodable message: ", buf, " error: ", err)
//		return
//	}
//	fmt.Println("parham log : sending message to down")
//	comCh.U2d <- msg
//	reply := <-comCh.D2u
//	fmt.Println("parham log : recieved resp from down")
//	fmt.Println("parham log : send resp to smf")
//	pConn.SendPFCPMsg(reply)
//}

// HandlePFCPMsg handles different types of PFCP messages.
func (pConn *PFCPConn) HandlePFCPMsg(buf []byte, comCh CommunicationChannel) {
	var (
		reply message.Message
		err   error
		//tempmsg *message.SessionEstablishmentRequest
	)

	msg, err := message.Parse(buf)
	if err != nil {
		log.Errorln("Ignoring undecodable message: ", buf, " error: ", err)
		return
	}

	addr := pConn.RemoteAddr().String()
	msgType := msg.MessageTypeName()
	//fmt.Println("parham log start request from : ", addr, ", msg type : ", msgType)
	//fmt.Println(msg)
	//fmt.Println("parham log end request")
	m := metrics.NewMessage(msgType, "Incoming")

	switch msg.MessageType() {
	// Connection related messages
	case message.MsgTypeHeartbeatRequest:
		reply, err = pConn.handleHeartbeatRequest(msg)
	case message.MsgTypePFDManagementRequest:
		reply, err = pConn.handlePFDMgmtRequest(msg)
	case message.MsgTypeAssociationSetupRequest:
		reply, err = pConn.handleAssociationSetupRequest(msg, comCh)
		if reply != nil && err == nil && pConn.upf.enableHBTimer {
			go pConn.startHeartBeatMonitor()
		}
		// TODO: Cleanup sessions

	case message.MsgTypeAssociationReleaseRequest:
		reply, err = pConn.handleAssociationReleaseRequest(msg)
		defer pConn.Shutdown()

	// Session related messages
	case message.MsgTypeSessionEstablishmentRequest:
		//tempmsg, err = message.ParseSessionEstablishmentRequest(buf)
		//fmt.Println("parham log start ParseSessionEstablishmentRequest : ", &tempmsg)
		//fmt.Println("parham log end ParseSessionEstablishmentRequest : ")
		reply, err = pConn.handleSessionEstablishmentRequest(msg)
	case message.MsgTypeSessionModificationRequest:
		reply, err = pConn.handleSessionModificationRequest(msg)
	case message.MsgTypeSessionDeletionRequest:
		reply, err = pConn.handleSessionDeletionRequest(msg)
	case message.MsgTypeSessionReportResponse:
		err = pConn.handleSessionReportResponse(msg)

	// Incoming response messages
	// TODO: Session Report Request
	case message.MsgTypeAssociationSetupResponse, message.MsgTypeHeartbeatResponse:
		pConn.handleIncomingResponse(msg)

	default:
		log.Errorln("Message type: ", msgType, " is currently not supported")
		return
	}

	nodeID := pConn.nodeID.remote
	// Check for errors in handling the message
	if err != nil {
		m.Finish(nodeID, "Failure")
		log.Errorln("Error handling PFCP message type", msgType, "from:", addr, "nodeID:", nodeID, err)
	} else {
		m.Finish(nodeID, "Success")
		log.Traceln("Successfully processed", msgType, "from", addr, "nodeID:", nodeID)
	}

	//pConn.SaveMessages(m)

	if reply != nil {
		//replyType := reply.MessageTypeName()
		//fmt.Println("parham log start response to : ", addr, " msg type : ", replyType)
		//fmt.Println(reply)
		//fmt.Println("parham log end response")
		pConn.SendPFCPMsg(reply)
	}
}

func (pConn *PFCPConn) SendPFCPMsg(msg message.Message) {
	addr := pConn.RemoteAddr().String()
	//nodeID := pConn.nodeID.remote
	msgType := msg.MessageTypeName()

	//m := metrics.NewMessage(msgType, "Outgoing")
	//defer pConn.SaveMessages(m)

	out := make([]byte, msg.MarshalLen())

	if err := msg.MarshalTo(out); err != nil {
		//m.Finish(nodeID, "Failure")
		log.Errorln("Failed to marshal", msgType, "for", addr, err)

		return
	}

	if _, err := pConn.Write(out); err != nil {
		//m.Finish(nodeID, "Failure")
		log.Errorln("Failed to transmit", msgType, "to", addr, err)

		return
	}

	//m.Finish(nodeID, "Success")
	log.Traceln("Sent", msgType, "to", addr)
}

func (pConn *PFCPConn) sendPFCPRequestMessage(r *Request) (message.Message, bool) {
	pConn.pendingReqs.Store(r.msg.Sequence(), r)

	pConn.SendPFCPMsg(r.msg)
	retriesLeft := pConn.upf.maxReqRetries

	for {
		if reply, rc := r.GetResponse(pConn.shutdown, pConn.upf.respTimeout); rc {
			log.Traceln("Request Timeout, retriesLeft:", retriesLeft, " , time : ", pConn.upf.readTimeout)

			if retriesLeft > 0 {
				pConn.SendPFCPMsg(r.msg)
				retriesLeft--
			} else {
				return nil, true
			}
		} else {
			return reply, false
		}
	}
}
