// SPDX-License-Identifier: Apache-2.0
// Copyright 2022-present Open Networking Foundation

package pfcpiface

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omec-project/upf-epc/internal/p4constants"
	log "github.com/sirupsen/logrus"

	"net"
	"time"

	"encoding/json"
	"io"
	"os"
)

const (
	// Default values
	maxReqRetriesDefault = 5
	respTimeoutDefault   = 2 * time.Second
	hbIntervalDefault    = 5 * time.Second
	readTimeoutDefault   = 15 * time.Second
)

// Conf : Json conf struct.
type Conf struct {
	Mode                   string           `json:"mode"`
	AccessIface            IfaceType        `json:"access"`
	CoreIface              IfaceType        `json:"core"`
	CPIface                CPIfaceInfo      `json:"cpiface"`
	P4rtcIface             P4rtcInfo        `json:"p4rtciface"`
	EnableP4rt             bool             `json:"enable_p4rt"`
	EnableFlowMeasure      bool             `json:"measure_flow"`
	SimInfo                SimModeInfo      `json:"sim"`
	ConnTimeout            uint32           `json:"conn_timeout"` // TODO(max): unused, remove
	ReadTimeout            uint32           `json:"read_timeout"` // TODO(max): convert to duration string
	EnableNotifyBess       bool             `json:"enable_notify_bess"`
	EnableEndMarker        bool             `json:"enable_end_marker"`
	NotifySockAddr         string           `json:"notify_sockaddr"`
	EndMarkerSockAddr      string           `json:"endmarker_sockaddr"`
	LogLevel               log.Level        `json:"log_level"`
	QciQosConfig           []QciQosConfig   `json:"qci_qos_config"`
	SliceMeterConfig       SliceMeterConfig `json:"slice_rate_limit_config"`
	MaxReqRetries          uint8            `json:"max_req_retries"`
	RespTimeout            string           `json:"resp_timeout"`
	EnableHBTimer          bool             `json:"enable_hbTimer"`
	HeartBeatInterval      string           `json:"heart_beat_interval"`
	MaxSessionsThreshold   uint32           `json:"max_sessions_threshold"`
	MinSessionsThreshold   uint32           `json:"min_sessions_threshold"`
	MaxSessionstolerance   float32          `json:"max_sessions_tolerance"`
	MinSessionstolerance   float32          `json:"min_sessions_tolerance"`
	MaxCPUThreshold        uint32           `json:"max_cpu_threshold"`
	MinCPUThreshold        uint32           `json:"min_cpu_threshold"`
	MaxBitRateThreshold    uint64           `json:"max_bitrate_threshold"`
	MinBitRateThreshold    uint64           `json:"min_bitrate_threshold"`
	ReconciliationInterval uint32           `json:"reconciliation_interval"`
	AutoScaleOut           bool             `json:"auto_scale_out"`
	AutoScaleIn            bool             `json:"auto_scale_in"`
	ScaleByCPU             bool             `json:"scalebycpu"`
	ScaleBySession         bool             `json:"scalebysession"`
	ScaleByBitRate         bool             `json:"scalebybitrate"`
	InitUPFs               uint32           `json:"init_upfs"`
	MinUPFs                uint32           `json:"min_upfs"`
	MaxUPFs                uint32           `json:"max_upfs"`
}

// QciQosConfig : Qos configured attributes.
type QciQosConfig struct {
	QCI                uint8  `json:"qci"`
	CBS                uint32 `json:"cbs"`
	PBS                uint32 `json:"pbs"`
	EBS                uint32 `json:"ebs"`
	BurstDurationMs    uint32 `json:"burst_duration_ms"`
	SchedulingPriority uint32 `json:"priority"`
}

type SliceMeterConfig struct {
	N6RateBps    uint64 `json:"n6_bps"`
	N6BurstBytes uint64 `json:"n6_burst_bytes"`
	N3RateBps    uint64 `json:"n3_bps"`
	N3BurstBytes uint64 `json:"n3_burst_bytes"`
}

// SimModeInfo : Sim mode attributes.
type SimModeInfo struct {
	MaxSessions uint32 `json:"max_sessions"`
	StartUEIP   net.IP `json:"start_ue_ip"`
	StartENBIP  net.IP `json:"start_enb_ip"`
	StartAUPFIP net.IP `json:"start_aupf_ip"`
	N6AppIP     net.IP `json:"n6_app_ip"`
	N9AppIP     net.IP `json:"n9_app_ip"`
	StartN3TEID string `json:"start_n3_teid"`
	StartN9TEID string `json:"start_n9_teid"`
}

// CPIfaceInfo : CPIface interface settings.
type CPIfaceInfo struct {
	Peers           []string `json:"peers"`
	UseFQDN         bool     `json:"use_fqdn"`
	NodeID          string   `json:"hostname"`
	HTTPPort        string   `json:"http_port"`
	Dnn             string   `json:"dnn"`
	EnableUeIPAlloc bool     `json:"enable_ue_ip_alloc"`
	UEIPPool        string   `json:"ue_ip_pool"`
}

// IfaceType : Gateway interface struct.
type IfaceType struct {
	IfName string `json:"ifname"`
}

// P4rtcInfo : P4 runtime interface settings.
type P4rtcInfo struct {
	SliceID             uint8           `json:"slice_id"`
	AccessIP            string          `json:"access_ip"`
	P4rtcServer         string          `json:"p4rtc_server"`
	P4rtcPort           string          `json:"p4rtc_port"`
	QFIToTC             map[uint8]uint8 `json:"qfi_tc_mapping"`
	DefaultTC           uint8           `json:"default_tc"`
	ClearStateOnRestart bool            `json:"clear_state_on_restart"`
}

// validateConf checks that the given config reaches a baseline of correctness.
func validateConf(conf Conf) error {
	if conf.EnableP4rt {
		_, _, err := net.ParseCIDR(conf.P4rtcIface.AccessIP)
		if err != nil {
			return ErrInvalidArgumentWithReason("conf.P4rtcIface.AccessIP", conf.P4rtcIface.AccessIP, err.Error())
		}

		_, _, err = net.ParseCIDR(conf.CPIface.UEIPPool)
		if err != nil {
			return ErrInvalidArgumentWithReason("conf.UEIPPool", conf.CPIface.UEIPPool, err.Error())
		}

		if conf.Mode != "" {
			return ErrInvalidArgumentWithReason("conf.Mode", conf.Mode, "mode must not be set for UP4")
		}
	} else {
		// Mode is only relevant in a BESS deployment.
		validModes := map[string]struct{}{
			"af_xdp":    {},
			"af_packet": {},
			"cndp":      {},
			"dpdk":      {},
			"sim":       {},
		}
		if _, ok := validModes[conf.Mode]; !ok {
			return ErrInvalidArgumentWithReason("conf.Mode", conf.Mode, "invalid mode")
		}
	}

	if conf.CPIface.EnableUeIPAlloc {
		_, _, err := net.ParseCIDR(conf.CPIface.UEIPPool)
		if err != nil {
			return ErrInvalidArgumentWithReason("conf.UEIPPool", conf.CPIface.UEIPPool, err.Error())
		}
	}

	for _, peer := range conf.CPIface.Peers {
		ip := net.ParseIP(peer)
		if ip == nil {
			return ErrInvalidArgumentWithReason("conf.CPIface.Peers", peer, "invalid IP")
		}
	}

	if _, err := time.ParseDuration(conf.RespTimeout); err != nil {
		return ErrInvalidArgumentWithReason("conf.RespTimeout", conf.RespTimeout, "invalid duration")
	}

	if conf.ReadTimeout == 0 {
		return ErrInvalidArgumentWithReason("conf.ReadTimeout", conf.ReadTimeout, "invalid duration")
	}

	if conf.MaxReqRetries == 0 {
		return ErrInvalidArgumentWithReason("conf.MaxReqRetries", conf.MaxReqRetries, "invalid number of retries")
	}

	if conf.EnableHBTimer {
		if _, err := time.ParseDuration(conf.HeartBeatInterval); err != nil {
			return err
		}
	}

	return nil
}

// LoadConfigFile : parse json file and populate corresponding struct.
func LoadConfigFile(filepath string) (Conf, error) {
	// Open up file.
	jsonFile, err := os.Open(filepath)
	if err != nil {
		return Conf{}, err
	}
	defer jsonFile.Close()

	// Read our file into memory.
	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		return Conf{}, err
	}

	var conf Conf
	//conf.LogLevel = log.InfoLevel
	conf.P4rtcIface.DefaultTC = uint8(p4constants.EnumTrafficClassElastic)

	err = json.Unmarshal(byteValue, &conf)
	if err != nil {
		return Conf{}, err
	}

	// Set defaults, when missing.
	if conf.RespTimeout == "" {
		conf.RespTimeout = respTimeoutDefault.String()
	}

	if conf.ReadTimeout == 0 {
		conf.ReadTimeout = uint32(readTimeoutDefault.Seconds())
	}

	if conf.MaxSessionsThreshold == 0 {
		conf.MaxSessionsThreshold = uint32(100000)
	}

	if conf.MaxCPUThreshold == 0 {
		conf.MaxCPUThreshold = uint32(2000)
	}

	if conf.MaxBitRateThreshold == 0 {
		conf.MaxBitRateThreshold = uint64(10000000000)
	}

	if conf.MaxSessionstolerance == 0 {
		conf.MaxSessionstolerance = float32(0.5)
	}

	if conf.ReconciliationInterval == 0 {
		conf.ReconciliationInterval = uint32(10)
	}

	if conf.MaxUPFs == 0 {
		conf.MaxUPFs = uint32(10)
	}

	if conf.MinUPFs == 0 {
		conf.MinUPFs = uint32(2)
	}

	if conf.InitUPFs == 0 {
		conf.InitUPFs = uint32(2)
	}

	if conf.MaxReqRetries == 0 {
		conf.MaxReqRetries = maxReqRetriesDefault
	}

	if conf.EnableHBTimer {
		if conf.HeartBeatInterval == "" {
			conf.HeartBeatInterval = hbIntervalDefault.String()
		}
	}

	// Perform basic validation.
	err = validateConf(conf)
	if err != nil {
		return Conf{}, err
	}

	changeUPFResources(conf)
	return conf, nil
}

func changeUPFResources(conf Conf) {
	N3BurstBytesStr := strconv.FormatUint(uint64(conf.SliceMeterConfig.N3BurstBytes), 10)
	N3RateBpsStr := strconv.FormatUint(uint64(conf.SliceMeterConfig.N3RateBps), 10)
	N6BurstBytesStr := strconv.FormatUint(uint64(conf.SliceMeterConfig.N6BurstBytes), 10)
	N6RateBpsStr := strconv.FormatUint(uint64(conf.SliceMeterConfig.N6RateBps), 10)
	N3BurstBytesPH := `$(n3_bps)`
	N3RateBpsPH := `$(n3_burst_bytes)`
	N6BurstBytesPH := `$(n6_bps)`
	N6RateBpsHP := `$(n6_burst_bytes)`

	var upfName string

	for i := 1; i <= int(conf.MaxUPFs); i++ {
		if i < 10 {
			upfName = fmt.Sprint("upf10", i)
		} else if i < 100 {
			upfName = fmt.Sprint("upf1", i)
		} else if i >= 100 {
			upfName = fmt.Sprint("upf", i)
		}
		upfFile := fmt.Sprint("/upfs/", upfName, ".yaml")

		content, err := os.ReadFile(upfFile)
		if err != nil {
			panic(err)
		}

		fileContent := string(content)

		updatedContent := strings.Replace(fileContent, N3BurstBytesPH, N3BurstBytesStr, -1)
		updatedContent = strings.Replace(updatedContent, N3RateBpsPH, N3RateBpsStr, -1)
		updatedContent = strings.Replace(updatedContent, N6BurstBytesPH, N6BurstBytesStr, -1)
		updatedContent = strings.Replace(updatedContent, N6RateBpsHP, N6RateBpsStr, -1)

		err = os.WriteFile(upfFile, []byte(updatedContent), 0644)
		if err != nil {
			panic(err)
		}
	}
}
