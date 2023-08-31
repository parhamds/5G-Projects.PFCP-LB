// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Intel Corporation
// Copyright 2022-present Open Networking Foundation

package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/omec-project/upf-epc/pfcpiface"
	log "github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"
)

var (
	UPAconfigPath = flag.String("config", "upf.json", "path to upf config")
)

func init() {
	// Set up logger
	log.SetReportCaller(true)
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
}

func main() {
	// cmdline args
	flag.Parse()
	ip_str := pfcpiface.GetLocalIP()
	fmt.Println("parham log : local IP = ", ip_str)
	comCh := pfcpiface.CommunicationChannel{
		U2d:              make(chan []byte, 100),
		D2u:              make(chan []byte, 100),
		UpfD2u:           make(chan *pfcpiface.Upf, 100),
		SesEstU2d:        make(chan *pfcpiface.SesEstU2dMsg, 100),
		SesEstRespCuzD2U: make(chan *ie.IE, 100),
		SesModU2d:        make(chan *pfcpiface.SesModU2dMsg, 100),
		SesModRespCuzD2U: make(chan *ie.IE, 100),
		SesDelU2d:        make(chan *pfcpiface.SesDelU2dMsg, 100),
		SesDelRespCuzD2U: make(chan *ie.IE, 100),
	}

	// Read and parse json startup file.
	conf, err := pfcpiface.LoadConfigFile(*UPAconfigPath)
	if err != nil {
		log.Fatalln("Error reading conf file:", err)
	}

	log.SetLevel(conf.LogLevel)

	log.Infof("%+v", conf)

	upaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Up)
	dpaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Down)

	// blocking
	//fmt.Println("parham log: calling upaPfcpi.Run for up")
	go upaPfcpi.Run(comCh, pfcpiface.Up)
	//fmt.Println("parham log: calling upaPfcpi.Run for down")
	dpaPfcpi.Run(comCh, pfcpiface.Down)
	time.Sleep(5 * time.Minute)
}
