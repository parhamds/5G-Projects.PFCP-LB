// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Intel Corporation
// Copyright 2022-present Open Networking Foundation

package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
	ip_str := pfcpiface.GetLocalIP()
	fmt.Println("parham log : local IP = ", ip_str)
	comCh := pfcpiface.CommunicationChannel{
		U2d:           make(chan []byte, 100),
		D2u:           make(chan []byte, 100),
		UpfD2u:        make(chan *pfcpiface.PfcpInfo, 100),
		SesEstU2d:     make(chan *pfcpiface.SesEstU2dMsg, 100),
		SesModU2d:     make(chan *pfcpiface.SesModU2dMsg, 100),
		SesDelU2d:     make(chan *pfcpiface.SesDelU2dMsg, 100),
		ResetSessions: make(chan struct{}, 100),
	}

	// Read and parse json startup file.
	conf, err := pfcpiface.LoadConfigFile(*UPAconfigPath)
	if err != nil {
		log.Fatalln("Error reading conf file:", err)
	}

	log.SetLevel(conf.LogLevel)

	//log.Infof("%+v", conf)

	upaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Up)
	dpaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Down)

	go func() {
		for {
			time.Sleep(1 * time.Second)
			//cmd := exec.Command("kubectl", "exec", "-n", "omec", "-it", "upf101-0", "--", "cat", "/proc/net/dev", "|", "grep", "access")
			cmd := exec.Command("sh", "-c", "kubectl exec -n omec -it upf101-0 -c pfcp-agent -- cat /proc/net/dev | grep access")
			fmt.Println("running command : ", cmd.String())
			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Println(err)
				continue
			}
			var receivedBytes int
			fields := strings.Fields(string(output))
			if len(fields) >= 2 {
				receivedBytesStr := fields[1]
				receivedBytes, err = strconv.Atoi(receivedBytesStr)
				if err != nil {
					fmt.Println("Error parsing received bytes:", err)
					return
				}
			}
			fmt.Printf("Received Bytes on 'access' interface: %d\n", receivedBytes)
		}
	}()

	go func(conf *pfcpiface.Conf) {
		time.Sleep(20 * time.Second)
		err = pfcpiface.RunUPFs(conf)
		if err != nil {
			log.Fatalln("Error creating UPFs:", err)
		}
	}(&conf)

	// blocking
	//fmt.Println("parham log: calling upaPfcpi.Run for up")
	go upaPfcpi.Run(comCh, pfcpiface.Up)
	//fmt.Println("parham log: calling upaPfcpi.Run for down")
	dpaPfcpi.Run(comCh, pfcpiface.Down)
	time.Sleep(5 * time.Minute)
}
