// SPDX-License-Identifier: Apache-2.0
// Copyright 2022-present Open Networking Foundation

package pfcpiface

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

type Position int

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

	//pfcpIface.upf = NewUPF(&conf, pfcpIface.fp)

	return pfcpIface
}

func (p *PFCPIface) mustInit(u2d, d2u chan []byte, pos Position) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if pos == Up {
		fmt.Println("parham log: calling NewPFCPNode for up")
	} else {
		fmt.Println("parham log: calling NewPFCPNode for down")
	}

	p.node = NewPFCPNode(pos, p.upf) //p.upf,

	httpMux := http.NewServeMux()

	setupConfigHandler(httpMux, p.upf)

	var err error

	p.uc, p.nc, err = setupProm(httpMux, p.upf, p.node)

	if err != nil {
		log.Fatalln("setupProm failed", err)
	}

	// Note: due to error with golangci-lint ("Error: G112: Potential Slowloris Attack
	// because ReadHeaderTimeout is not configured in the http.Server (gosec)"),
	// the ReadHeaderTimeout is set to the same value as in nginx (client_header_timeout)
	p.httpSrv = &http.Server{Addr: p.httpEndpoint, Handler: httpMux, ReadHeaderTimeout: 60 * time.Second}
}

func (p *PFCPIface) Run(u2d, d2u chan []byte, pos Position) {
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
	p.mustInit(u2d, d2u, pos)

	go func() {
		if err := p.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln("http server failed", err)
		}

		log.Infoln("http server closed")
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	signal.Notify(sig, syscall.SIGTERM)

	go func() {
		oscall := <-sig
		log.Infof("System call received: %+v", oscall)
		p.Stop()
	}()

	// blocking
	if pos == Up {
		fmt.Println("parham log: calling Serve for up")
	} else {
		fmt.Println("parham log: calling Serve for down")
	}
	p.node.Serve(u2d, d2u, pos)
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
