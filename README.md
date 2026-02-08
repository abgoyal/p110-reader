# p110 - Tapo P110/P115 Smart Plug Monitor

A Go command-line tool for monitoring TP-Link Tapo P110/P115 smart plugs with energy monitoring. Features auto-discovery, real-time power readings, historical data collection, and local SQLite storage to preserve data beyond device limits.

## Features

- **Auto-discovery**: Finds Tapo devices on your network automatically
- **Real-time monitoring**: Current power consumption, today's usage, monthly totals
- **Historical data**: Hourly, daily, and monthly energy breakdowns
- **Daemon mode**: Background service that collects and archives data
- **Local storage**: SQLite database preserves data the device forgets
- **Cost calculation**: Optional electricity rate for cost estimates
- **Multiple devices**: Monitor all devices on your network

## Building

```bash
cd golang
go build -o p110 ./cmd/p110/
```

## Usage

### Environment Variables

Set credentials to avoid typing them each time:

```bash
export TAPO_USERNAME="your-email@example.com"
export TAPO_PASSWORD="your-password"
```

### Basic Query

```bash
# Auto-discover and query first device found
./p110

# Query specific device by IP
./p110 -ip 192.168.1.100

# Query all devices on network
./p110 -all

# Discovery only (no credentials needed)
./p110 -discover
```

### Output Formats

```bash
# Human-readable summary with charts (default)
./p110

# Raw JSON data
./p110 -json

# Verbose raw output
./p110 -raw
```

### Cost Calculation

```bash
# Show costs with electricity rate (per kWh)
./p110 -rate 8.5 -currency "₹"
./p110 -rate 0.12 -currency "$"
```

### Daemon Mode

Run as a background service to collect data periodically:

```bash
# Start daemon with default 5-minute interval
./p110 -daemon

# Custom interval and database path
./p110 -daemon -interval 1m -db /path/to/data.db

# Monitor all devices
./p110 -daemon -all
```

The daemon:
- Polls devices at the specified interval (default: 5 minutes)
- Stores power readings for detailed history
- Archives hourly data before the device resets it at midnight
- Archives daily data before the device forgets it (~90 days)
- Archives monthly data before yearly reset
- Handles SIGINT/SIGTERM gracefully

### View Historical Data

```bash
# View collected data from database
./p110 -history

# Show more days of history
./p110 -history -days 30

# With cost calculation
./p110 -history -rate 8.5 -currency "₹"
```

## Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-username` | `$TAPO_USERNAME` | Tapo account email |
| `-password` | `$TAPO_PASSWORD` | Tapo account password |
| `-ip` | (auto-discover) | Device IP address |
| `-all` | false | Query all discovered devices |
| `-discover` | false | Only list devices, don't connect |
| `-json` | false | JSON output format |
| `-raw` | false | Verbose raw output |
| `-rate` | 0 | Electricity rate per kWh |
| `-currency` | ₹ | Currency symbol |
| `-timeout` | 5s | Discovery timeout |
| `-daemon` | false | Run in daemon mode |
| `-interval` | 5m | Daemon polling interval |
| `-db` | p110.db | SQLite database path |
| `-history` | false | View historical data |
| `-days` | 7 | Days of history to show |

## Database Schema

The daemon stores data in SQLite with these tables:

### readings
Frequent power snapshots (every poll interval):
- `timestamp` - When the reading was taken
- `device_ip` - Device IP address
- `device_mac` - Device MAC address
- `power_mw` - Power in milliwatts

### hourly
Archived hourly energy data:
- `date` - Date (YYYY-MM-DD)
- `hour` - Hour (0-23)
- `device_ip` - Device IP address
- `energy_wh` - Energy in watt-hours

### daily
Archived daily energy data:
- `date` - Date (YYYY-MM-DD)
- `device_ip` - Device IP address
- `energy_wh` - Energy in watt-hours
- `runtime_min` - Runtime in minutes

### monthly
Archived monthly energy data:
- `year` - Year
- `month` - Month (1-12)
- `device_ip` - Device IP address
- `energy_wh` - Energy in watt-hours

## Device Data Retention

The P110 device has limited memory:
- **Hourly data**: Resets at midnight (lost next day)
- **Daily data**: Keeps ~90 days
- **Monthly data**: Resets at year boundary

The daemon preserves this data locally so you don't lose historical information.

## Running as a System Service

Example systemd service file (`/etc/systemd/system/p110.service`):

```ini
[Unit]
Description=Tapo P110 Energy Monitor
After=network.target

[Service]
Type=simple
User=your-user
Environment="TAPO_USERNAME=your-email@example.com"
Environment="TAPO_PASSWORD=your-password"
ExecStart=/path/to/p110 -daemon -all -db /var/lib/p110/data.db
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable p110
sudo systemctl start p110
```

## Tapo Protocol Documentation

This implementation supports the KLAP (Key-Length-Authentication Protocol) used by newer Tapo firmware. The protocol details are documented here for reference.

### Device Discovery

Devices are discovered via UDP broadcast:

1. Send a 16-byte magic packet to `255.255.255.255:20002`:
   ```
   02 00 00 01 00 00 00 00 00 00 00 00 46 3c b5 d3
   ```

2. Devices respond with a JSON payload (after 16-byte header):
   ```json
   {
     "result": {
       "device_id": "...",
       "device_model": "P110",
       "ip": "192.168.1.100",
       "mac": "AA-BB-CC-DD-EE-FF",
       "mgt_encrypt_schm": {
         "encrypt_type": "KLAP",
         "http_port": 80
       }
     }
   }
   ```

### KLAP Authentication

KLAP uses a two-phase handshake to establish an encrypted session:

#### Auth Hash Generation
```
auth_hash = SHA256(SHA1(username) + SHA1(password))
```

#### Handshake 1: `POST /app/handshake1`
- **Send**: 16 random bytes (`local_seed`)
- **Receive**: 48 bytes = `remote_seed` (16 bytes) + `server_hash` (32 bytes)
- **Verify**: `server_hash == SHA256(local_seed + remote_seed + auth_hash)`
- **Cookie**: Server sets `TP_SESSIONID` cookie

#### Handshake 2: `POST /app/handshake2`
- **Send**: `SHA256(remote_seed + local_seed + auth_hash)`
- **Receive**: 200 OK (session established)

#### Key Derivation
After successful handshake, derive encryption keys:

```
local_hash = local_seed + remote_seed + auth_hash

key     = SHA256("lsk" + local_hash)[:16]      # AES-128 key
iv_seed = SHA256("iv" + local_hash)[:12]       # IV prefix
sig     = SHA256("ldk" + local_hash)[:28]      # Signature key
seq     = SHA256("iv" + local_hash)[28:32]     # Initial sequence (big-endian int32)
```

### Encrypted Requests

#### Request: `POST /app/request?seq={seq}`

1. Increment sequence number
2. Build 16-byte IV: `iv_seed (12 bytes) + seq (4 bytes big-endian)`
3. Encrypt payload with AES-128-CBC + PKCS7 padding
4. Calculate signature: `SHA256(sig + seq_bytes + ciphertext)`
5. Send: `signature (32 bytes) + ciphertext`

#### Response
1. Verify signature (same calculation)
2. Decrypt with AES-128-CBC using same IV construction
3. Parse JSON response

### API Methods

Common methods for P110/P115:

| Method | Description |
|--------|-------------|
| `get_device_info` | Device name, model, MAC, on/off state, signal |
| `get_device_usage` | Usage stats (time, power) for today/7d/30d |
| `get_current_power` | Current power draw in milliwatts |
| `get_energy_usage` | Today/month energy (Wh) and runtime |
| `get_energy_data` | Hourly/daily/monthly energy arrays |
| `set_device_info` | Control device (on/off) |

#### get_energy_data Parameters
```json
{
  "method": "get_energy_data",
  "params": {
    "start_timestamp": 1704067200,
    "end_timestamp": 1704153600,
    "interval": 60
  }
}
```

Intervals:
- `60` = hourly (returns 24 values for a day)
- `1440` = daily (returns days in quarter)
- `43200` = monthly (returns 12 values for year)

### References

- Protocol reverse-engineered from [mihai-dinculescu/tapo](https://github.com/mihai-dinculescu/tapo) (Rust)
- Python implementation: [python-kasa](https://github.com/python-kasa/python-kasa)

## License

MIT
