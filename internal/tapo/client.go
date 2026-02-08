package tapo

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Client is the main client for communicating with Tapo devices.
type Client struct {
	username string
	password string
}

// NewClient creates a new Tapo API client.
func NewClient(username, password string) *Client {
	return &Client{
		username: username,
		password: password,
	}
}

// P110 represents a connection to a P110/P115 smart plug.
type P110 struct {
	client       *Client
	ip           string
	session      *klapSession
	terminalUUID string
	mu           sync.Mutex
}

// Connect establishes a connection to a P110/P115 device.
func (c *Client) Connect(ip string) (*P110, error) {
	session, err := newKlapSession(ip, c.username, c.password)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	if err := session.handshake(); err != nil {
		return nil, fmt.Errorf("handshake failed: %w", err)
	}

	return &P110{
		client:       c,
		ip:           ip,
		session:      session,
		terminalUUID: uuid.New().String(),
	}, nil
}

// ConnectWithDiscovery discovers and connects to the first P110/P115 device found.
func (c *Client) ConnectWithDiscovery(ctx context.Context) (*P110, string, error) {
	device, err := DiscoverFirst(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("discovery failed: %w", err)
	}

	p110, err := c.Connect(device.IP)
	if err != nil {
		return nil, device.IP, fmt.Errorf("connection failed: %w", err)
	}

	return p110, device.IP, nil
}

// sendRequest sends a request to the device and returns the response.
func (p *P110) sendRequest(method string, params interface{}) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Build request - simplified format that Tapo devices expect
	req := map[string]interface{}{
		"method": method,
	}
	if params != nil {
		req["params"] = params
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	respJSON, err := p.session.request(reqJSON)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	var resp klapResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if resp.ErrorCode != 0 {
		return nil, fmt.Errorf("device returned error code: %d", resp.ErrorCode)
	}

	return resp.Result, nil
}

// GetDeviceInfo retrieves device information.
func (p *P110) GetDeviceInfo() (*DeviceInfo, error) {
	result, err := p.sendRequest("get_device_info", nil)
	if err != nil {
		return nil, err
	}

	var info DeviceInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, fmt.Errorf("failed to parse device info: %w", err)
	}

	return &info, nil
}

// GetDeviceUsage retrieves device usage statistics.
func (p *P110) GetDeviceUsage() (*DeviceUsage, error) {
	result, err := p.sendRequest("get_device_usage", nil)
	if err != nil {
		return nil, err
	}

	var usage DeviceUsage
	if err := json.Unmarshal(result, &usage); err != nil {
		return nil, fmt.Errorf("failed to parse device usage: %w", err)
	}

	return &usage, nil
}

// GetCurrentPower retrieves the current power consumption.
func (p *P110) GetCurrentPower() (*CurrentPower, error) {
	result, err := p.sendRequest("get_current_power", nil)
	if err != nil {
		return nil, err
	}

	var power CurrentPower
	if err := json.Unmarshal(result, &power); err != nil {
		return nil, fmt.Errorf("failed to parse current power: %w", err)
	}

	return &power, nil
}

// GetEnergyUsage retrieves energy usage data.
func (p *P110) GetEnergyUsage() (*EnergyUsage, error) {
	result, err := p.sendRequest("get_energy_usage", nil)
	if err != nil {
		return nil, err
	}

	var usage EnergyUsage
	if err := json.Unmarshal(result, &usage); err != nil {
		return nil, fmt.Errorf("failed to parse energy usage: %w", err)
	}

	return &usage, nil
}

// GetEnergyData retrieves energy data for the specified interval.
func (p *P110) GetEnergyData(interval EnergyDataInterval, t time.Time) (*EnergyData, error) {
	startTS, endTS := getStartEndTimestamps(interval, t)

	var intervalMinutes int
	switch interval {
	case EnergyDataHourly:
		intervalMinutes = IntervalHourly
	case EnergyDataDaily:
		intervalMinutes = IntervalDaily
	case EnergyDataMonthly:
		intervalMinutes = IntervalMonthly
	default:
		return nil, fmt.Errorf("invalid interval: %s", interval)
	}

	params := energyDataParams{
		StartTimestamp: startTS,
		EndTimestamp:   endTS,
		Interval:       intervalMinutes,
	}

	result, err := p.sendRequest("get_energy_data", params)
	if err != nil {
		return nil, err
	}

	var data EnergyData
	if err := json.Unmarshal(result, &data); err != nil {
		return nil, fmt.Errorf("failed to parse energy data: %w", err)
	}

	return &data, nil
}

// TurnOn turns the device on.
func (p *P110) TurnOn() error {
	_, err := p.sendRequest("set_device_info", map[string]bool{"device_on": true})
	return err
}

// TurnOff turns the device off.
func (p *P110) TurnOff() error {
	_, err := p.sendRequest("set_device_info", map[string]bool{"device_on": false})
	return err
}

// IP returns the IP address of the connected device.
func (p *P110) IP() string {
	return p.ip
}
