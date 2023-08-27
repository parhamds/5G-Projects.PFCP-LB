// SPDX-License-Identifier: Apache-2.0
// Copyright 2022-present Open Networking Foundation

package pfcpiface

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Position int

type CommunicationChannel struct {
	U2d chan []byte
	D2u chan []byte
}

const (
	Up Position = iota
	Down
)

var (
	simulate = simModeDisable
)

func init() {
	flag.Var(&simulate, "simulate", "create|delete|create_continue simulated sessions")
}

type PFCPIface struct {
	conf Conf

	node *PFCPNode
	fp   datapath
	upf  *upf

	httpSrv      *http.Server
	httpEndpoint string

	uc *upfCollector
	nc *PfcpNodeCollector

	mu sync.Mutex
}

func NewPFCPIface(conf Conf, pos Position) *PFCPIface {
	pfcpIface := &PFCPIface{
		conf: conf,
	}

	//if conf.EnableP4rt {
	//	pfcpIface.fp = &UP4{}
	//} else {
	//	pfcpIface.fp = &bess{}
	//}
	var httpPort string
	if pos == Up {
		httpPort = "8080"
	} else {
		httpPort = "8081"
	}

	if conf.CPIface.HTTPPort != "" {
		httpPort = conf.CPIface.HTTPPort
	}

	pfcpIface.httpEndpoint = ":" + httpPort

	pfcpIface.upf = NewUPF(&conf, pos) //pfcpIface.fp

	return pfcpIface
}

func (p *PFCPIface) mustInit(comch CommunicationChannel, pos Position) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if pos == Up {
		fmt.Println("parham log: calling NewPFCPNode for up")
	} else {
		fmt.Println("parham log: calling NewPFCPNode for down")
	}

	p.node = NewPFCPNode(pos, p.upf) //p.upf,

	//var err error

	//p.uc, p.nc, err = setupProm(httpMux, p.upf, p.node)

	//if err != nil {
	//	log.Fatalln("setupProm failed", err)
	//}

	//if pos == Down {
	//	httpMux := http.NewServeMux()
	//	setupConfigHandler(httpMux, p.upf)
	//	p.httpSrv = &http.Server{Addr: p.httpEndpoint, Handler: httpMux, ReadHeaderTimeout: 60 * time.Second}
	//}

}

func (p *PFCPIface) Run(comch CommunicationChannel, pos Position) {
	if simulate.enable() {
		p.upf.sim(simulate, &p.conf.SimInfo)

		fmt.Println("parham log : simulate.enable() is true")

		if !simulate.keepGoing() {
			return
		}
	}
	if pos == Up {
		fmt.Println("parham log: calling mustInit for up")
	} else {
		fmt.Println("parham log: calling mustInit for down")
	}
	p.mustInit(comch, pos)

	if pos == Down {
		//time.Sleep(10 * time.Minute)
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			newPFCPHandler(w, r, p.node)
		})
		server := http.Server{Addr: ":8081"}
		go func() {
			//fmt.Println("parham log : http server is serving")
			//if err := p.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			//	log.Fatalln("http server failed", err)
			//}
			//log.Infoln("http server closed")
			//http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			//	newPFCPHandler(w, r, p.upf)
			//})
			//fmt.Println("Server started on :8081")
			//http.ListenAndServe(":8081", nil)
			server.ListenAndServe()
		}()

		//sig := make(chan os.Signal, 1)
		//signal.Notify(sig, os.Interrupt)
		//signal.Notify(sig, syscall.SIGTERM)

		//go func() {
		//	oscall := <-sig
		//	log.Infof("System call received: %+v", oscall)
		//	server.Shutdown(nil)
		//}()
		//go func() {
		//	oscall := <-sig
		//	log.Infof("System call received: %+v", oscall)
		//	p.Stop()
		//}()
	}
	//time.Sleep(1 * time.Minute)
	// blocking

	if pos == Up {
		fmt.Println("parham log: calling Serve for up")
	} else {
		fmt.Println("parham log: calling Serve for down")
	}
	p.node.Serve(comch, pos)
}

// Stop sends cancellation signal to main Go routine and waits for shutdown to complete.
func (p *PFCPIface) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctxHttpShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		cancel()
	}()

	if err := p.httpSrv.Shutdown(ctxHttpShutdown); err != nil {
		log.Errorln("Failed to shutdown http: ", err)
	}

	p.node.Stop()

	// Wait for PFCP node shutdown
	p.node.Done()
}
