package tapo

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	discoveryPort    = 20002
	discoveryMagic   = "020000010000000000000000463cb5d3"
	discoveryTimeout = 5 * time.Second
)

// Discover finds Tapo devices on the local network using UDP broadcast.
// It returns a slice of discovered devices.
func Discover(ctx context.Context) ([]DiscoveredDevice, error) {
	return DiscoverWithTimeout(ctx, discoveryTimeout)
}

// DiscoverWithTimeout finds Tapo devices with a custom timeout.
func DiscoverWithTimeout(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	payload, err := hex.DecodeString(discoveryMagic)
	if err != nil {
		return nil, fmt.Errorf("failed to decode discovery payload: %w", err)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}
	defer conn.Close()

	broadcastAddr := &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: discoveryPort,
	}

	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set write deadline: %w", err)
	}

	_, err = conn.WriteToUDP(payload, broadcastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send discovery broadcast: %w", err)
	}

	devices := make([]DiscoveredDevice, 0)
	seen := make(map[string]bool)

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return devices, ctx.Err()
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return devices, fmt.Errorf("failed to set read deadline: %w", err)
		}

		buf := make([]byte, 2048)
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			continue
		}

		if n <= 16 {
			continue
		}

		// Skip the 16-byte header
		jsonData := buf[16:n]

		var resp discoveryResponse
		if err := json.Unmarshal(jsonData, &resp); err != nil {
			continue
		}

		if resp.ErrorCode != 0 {
			continue
		}

		ip := resp.Result.IP
		if ip == "" {
			ip = addr.IP.String()
		}

		if seen[ip] {
			continue
		}
		seen[ip] = true

		devices = append(devices, DiscoveredDevice{
			IP:       ip,
			MAC:      resp.Result.MAC,
			DeviceID: resp.Result.DeviceID,
			Model:    resp.Result.DeviceModel,
		})
	}

	return devices, nil
}

// DiscoverFirst finds the first Tapo device on the network.
// Returns an error if no device is found within the timeout.
func DiscoverFirst(ctx context.Context) (*DiscoveredDevice, error) {
	return DiscoverFirstWithTimeout(ctx, discoveryTimeout)
}

// DiscoverFirstWithTimeout finds the first device with a custom timeout.
func DiscoverFirstWithTimeout(ctx context.Context, timeout time.Duration) (*DiscoveredDevice, error) {
	payload, err := hex.DecodeString(discoveryMagic)
	if err != nil {
		return nil, fmt.Errorf("failed to decode discovery payload: %w", err)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}
	defer conn.Close()

	broadcastAddr := &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: discoveryPort,
	}

	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set write deadline: %w", err)
	}

	_, err = conn.WriteToUDP(payload, broadcastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send discovery broadcast: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		buf := make([]byte, 2048)
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			continue
		}

		if n <= 16 {
			continue
		}

		jsonData := buf[16:n]

		var resp discoveryResponse
		if err := json.Unmarshal(jsonData, &resp); err != nil {
			continue
		}

		if resp.ErrorCode != 0 {
			continue
		}

		ip := resp.Result.IP
		if ip == "" {
			ip = addr.IP.String()
		}

		return &DiscoveredDevice{
			IP:       ip,
			MAC:      resp.Result.MAC,
			DeviceID: resp.Result.DeviceID,
			Model:    resp.Result.DeviceModel,
		}, nil
	}

	return nil, fmt.Errorf("no Tapo devices found on the network")
}
