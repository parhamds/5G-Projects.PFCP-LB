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

func extractCPULoad(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return ""
	}

	// Extract the second line, which contains CPU load
	// Example: "mongodb-55bbb8c4c4-dg5v5   20m          149Mi"
	fields := strings.Fields(lines[1])
	if len(fields) >= 2 {
		return fields[1]
	}

	return ""
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

	go func() {
		for {
			time.Sleep(10 * time.Second)
			podName := "upf101-0"
			namespace := "omec"

			// Run the kubectl top pod command
			cmd := exec.Command("kubectl", "top", "pod", "-n", namespace, podName)
			fmt.Println("executing command : ", cmd.String())
			// Capture the command output
			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Println("Error running kubectl:", err)
				continue
			}

			// Extract the CPU load from the output
			cpuLoadStr := extractCPULoad(string(output))
			if cpuLoadStr != "" {
				cpuLoadTrimmed := strings.TrimRight(cpuLoadStr, "m")
				cpuLoad, err := strconv.Atoi(cpuLoadTrimmed)
				if err != nil {
					fmt.Println("Error converting CPU load to integer:", err)
					continue
				}
				fmt.Printf("CPU load as an integer: %d\n", cpuLoad)
			}
		}
	}()

	log.SetLevel(conf.LogLevel)

	//log.Infof("%+v", conf)

	upaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Up)
	dpaPfcpi := pfcpiface.NewPFCPIface(conf, pfcpiface.Down)

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
