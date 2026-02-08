package tapo

import (
	"encoding/json"
	"time"
)

// DeviceInfo contains information about a Tapo device.
type DeviceInfo struct {
	DeviceID              string `json:"device_id"`
	FirmwareVersion       string `json:"fw_ver"`
	HardwareVersion       string `json:"hw_ver"`
	Type                  string `json:"type"`
	Model                 string `json:"model"`
	MAC                   string `json:"mac"`
	HWID                  string `json:"hw_id"`
	FWID                  string `json:"fw_id"`
	OEMID                 string `json:"oem_id"`
	IP                    string `json:"ip"`
	TimeDiff              int    `json:"time_diff"`
	SSID                  string `json:"ssid"`
	RSSI                  int    `json:"rssi"`
	SignalLevel           int    `json:"signal_level"`
	Latitude              int    `json:"latitude"`
	Longitude             int    `json:"longitude"`
	Lang                  string `json:"lang"`
	Avatar                string `json:"avatar"`
	Region                string `json:"region"`
	Specs                 string `json:"specs"`
	Nickname              string `json:"nickname"`
	HasSetLocationInfo    bool   `json:"has_set_location_info"`
	DeviceON              bool   `json:"device_on"`
	OnTime                int    `json:"on_time"`
	OverHeated            bool   `json:"overheated"`
	PowerProtectionStatus string `json:"power_protection_status"`
	Location              string `json:"location"`
}

// DeviceUsage contains usage statistics for a Tapo device.
type DeviceUsage struct {
	TimeUsage  UsageEntry `json:"time_usage"`
	PowerUsage UsageEntry `json:"power_usage"`
	SavedPower UsageEntry `json:"saved_power"`
}

// UsageEntry contains usage data for different time periods.
type UsageEntry struct {
	Today  int `json:"today"`
	Past7  int `json:"past7"`
	Past30 int `json:"past30"`
}

// CurrentPower contains the current power consumption.
type CurrentPower struct {
	CurrentPower int `json:"current_power"` // in milliwatts
}

// EnergyUsage contains energy usage data.
type EnergyUsage struct {
	TodayRuntime      int    `json:"today_runtime"` // minutes
	MonthRuntime      int    `json:"month_runtime"` // minutes
	TodayEnergy       int    `json:"today_energy"`  // Wh
	MonthEnergy       int    `json:"month_energy"`  // Wh
	LocalTime         string `json:"local_time"`
	ElectricityCharge []int  `json:"electricity_charge"`
	CurrentPower      int    `json:"current_power"` // mW
}

// EnergyDataInterval represents the interval for energy data queries.
type EnergyDataInterval string

const (
	EnergyDataHourly  EnergyDataInterval = "hourly"
	EnergyDataDaily   EnergyDataInterval = "daily"
	EnergyDataMonthly EnergyDataInterval = "monthly"
)

// EnergyData contains energy data for a specific interval.
type EnergyData struct {
	LocalTime      string `json:"local_time"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
	Interval       int    `json:"interval"`
	Data           []int  `json:"data"` // Wh values
}

// DiscoveredDevice represents a device found during discovery.
type DiscoveredDevice struct {
	IP       string
	MAC      string
	DeviceID string
	Model    string
	Alias    string
}

// discoveryResponse is the raw response from UDP discovery.
type discoveryResponse struct {
	Result struct {
		DeviceID       string `json:"device_id"`
		Owner          string `json:"owner"`
		DeviceType     string `json:"device_type"`
		DeviceModel    string `json:"device_model"`
		IP             string `json:"ip"`
		MAC            string `json:"mac"`
		IsSupportIOTC  bool   `json:"is_support_iotcloud"`
		OBDSrc         string `json:"obd_src"`
		FactoryDefault bool   `json:"factory_default"`
		MgtEncryptSchm struct {
			IsSupportHTTPS bool   `json:"is_support_https"`
			EncryptType    string `json:"encrypt_type"`
			HTTPPort       int    `json:"http_port"`
			LV             int    `json:"lv"`
		} `json:"mgt_encrypt_schm"`
	} `json:"result"`
	ErrorCode int `json:"error_code"`
}

// klapRequest is the request format for KLAP protocol.
type klapRequest struct {
	Method          string      `json:"method"`
	Params          interface{} `json:"params,omitempty"`
	RequestTimeMils int64       `json:"requestTimeMils"`
	TerminalUUID    string      `json:"terminalUUID"`
}

// klapResponse is the response format from KLAP protocol.
type klapResponse struct {
	ErrorCode int             `json:"error_code"`
	Result    json.RawMessage `json:"result"`
}

// energyDataParams contains parameters for energy data requests.
type energyDataParams struct {
	StartTimestamp int64 `json:"start_timestamp"`
	EndTimestamp   int64 `json:"end_timestamp"`
	Interval       int   `json:"interval"`
}

// Interval constants for energy data requests (in minutes).
const (
	IntervalHourly  = 60    // 60 minutes
	IntervalDaily   = 1440  // 24 hours
	IntervalMonthly = 43200 // 30 days
)

// getStartEndTimestamps calculates start/end timestamps for energy data queries.
func getStartEndTimestamps(interval EnergyDataInterval, t time.Time) (int64, int64) {
	loc := t.Location()

	switch interval {
	case EnergyDataHourly:
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		end := start.Add(24 * time.Hour)
		return start.Unix(), end.Unix()
	case EnergyDataDaily:
		quarterStart := getQuarterStartMonth(t)
		start := time.Date(t.Year(), time.Month(quarterStart), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 3, 0)
		return start.Unix(), end.Unix()
	case EnergyDataMonthly:
		start := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc)
		end := time.Date(t.Year()+1, 1, 1, 0, 0, 0, 0, loc)
		return start.Unix(), end.Unix()
	default:
		return 0, 0
	}
}

func getQuarterStartMonth(t time.Time) int {
	return 3*((int(t.Month())-1)/3) + 1
}
