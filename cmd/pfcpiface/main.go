// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Intel Corporation
// Copyright 2022-present Open Networking Foundation

package main

import (
	"flag"
	"time"

	"github.com/omec-project/upf-epc/pfcpiface"
	log "github.com/sirupsen/logrus"
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
	u2d := make(chan []byte, 100)
	d2u := make(chan []byte, 100)
	// Read and parse json startup file.
	upaConf, err := pfcpiface.LoadConfigFile(*UPAconfigPath)
	dpaConf, err := pfcpiface.LoadConfigFile(*UPAconfigPath)
	if err != nil {
		log.Fatalln("Error reading conf file:", err)
	}

	log.SetLevel(upaConf.LogLevel)

	log.Infof("%+v", upaConf)

	upaPfcpi := pfcpiface.NewPFCPIface(upaConf)
	dpaPfcpi := pfcpiface.NewPFCPIface(dpaConf)

	// blocking
	go upaPfcpi.Run(u2d, d2u, pfcpiface.Up)
	dpaPfcpi.Run(u2d, d2u, pfcpiface.Down)
	time.Sleep(5 * time.Minute)
}
