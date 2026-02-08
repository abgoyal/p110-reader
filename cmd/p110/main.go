package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/abhishek/p110/internal/store"
	"github.com/abhishek/p110/internal/tapo"
)

type outputMode int

const (
	modeSummary outputMode = iota
	modeRaw
	modeJSON
)

func main() {
	// Connection flags
	username := flag.String("username", "", "Tapo account username (email)")
	password := flag.String("password", "", "Tapo account password")
	ip := flag.String("ip", "", "Device IP address (optional, will auto-discover if not provided)")
	timeout := flag.Duration("timeout", 5*time.Second, "Discovery timeout")

	// Query mode flags
	all := flag.Bool("all", false, "Query all discovered devices")
	discover := flag.Bool("discover", false, "Only discover devices, don't connect")
	jsonOutput := flag.Bool("json", false, "Output in JSON format")
	raw := flag.Bool("raw", false, "Output raw data (verbose)")

	// Control flags
	turnOn := flag.Bool("on", false, "Turn device on (requires -ip)")
	turnOff := flag.Bool("off", false, "Turn device off (requires -ip)")

	// Display flags
	rate := flag.Float64("rate", 0, "Electricity rate per kWh for cost calculation")
	currency := flag.String("currency", "₹", "Currency symbol for cost display")

	// Daemon mode flags
	daemon := flag.Bool("daemon", false, "Run in daemon mode, periodically collecting data")
	interval := flag.Duration("interval", 5*time.Minute, "Polling interval for daemon mode")
	dbPath := flag.String("db", "p110.db", "SQLite database path for daemon mode")

	// History viewing flags
	history := flag.Bool("history", false, "View historical data from database")
	days := flag.Int("days", 7, "Number of days of history to show")

	flag.Parse()

	// Check environment variables if flags not provided
	if *username == "" {
		*username = os.Getenv("TAPO_USERNAME")
	}
	if *password == "" {
		*password = os.Getenv("TAPO_PASSWORD")
	}

	// Validate mutually exclusive flags
	if *turnOn && *turnOff {
		fmt.Fprintln(os.Stderr, "Error: cannot specify both -on and -off")
		os.Exit(1)
	}

	// Determine output mode
	mode := modeSummary
	if *jsonOutput {
		mode = modeJSON
	} else if *raw {
		mode = modeRaw
	}

	// Daemon mode
	if *daemon {
		runDaemon(*username, *password, *ip, *all, *dbPath, *interval, *timeout)
		return
	}

	// History viewing mode
	if *history {
		showHistory(*dbPath, *ip, *days, *rate, *currency)
		return
	}

	// Normal query mode
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Discovery-only mode
	if *discover {
		devices, err := tapo.DiscoverWithTimeout(ctx, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Discovery failed: %v\n", err)
			os.Exit(1)
		}

		if len(devices) == 0 {
			fmt.Println("No devices found")
			os.Exit(0)
		}

		if mode == modeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(devices)
		} else {
			fmt.Printf("Found %d device(s):\n", len(devices))
			for _, d := range devices {
				fmt.Printf("  - IP: %s, Model: %s, MAC: %s\n", d.IP, d.Model, d.MAC)
			}
		}
		return
	}

	// Need credentials for full operation
	if *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "Error: username and password required")
		fmt.Fprintln(os.Stderr, "Provide via -username/-password flags or TAPO_USERNAME/TAPO_PASSWORD env vars")
		os.Exit(1)
	}

	client := tapo.NewClient(*username, *password)

	// Determine which devices to query
	var deviceIPs []string

	if *ip != "" {
		deviceIPs = []string{*ip}
	} else if *all {
		if mode != modeJSON {
			fmt.Println("Discovering devices...")
		}
		devices, err := tapo.DiscoverWithTimeout(ctx, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Discovery failed: %v\n", err)
			os.Exit(1)
		}
		if len(devices) == 0 {
			fmt.Println("No devices found")
			os.Exit(0)
		}
		if mode != modeJSON {
			fmt.Printf("Found %d device(s)\n\n", len(devices))
		}
		for _, d := range devices {
			deviceIPs = append(deviceIPs, d.IP)
		}
	} else {
		if mode != modeJSON {
			fmt.Println("Discovering devices...")
		}
		device, err := tapo.DiscoverFirst(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Discovery failed: %v\n", err)
			os.Exit(1)
		}
		deviceIPs = []string{device.IP}
	}

	// Control mode - turn device on/off
	if *turnOn || *turnOff {
		if len(deviceIPs) != 1 {
			fmt.Fprintln(os.Stderr, "Error: on/off control requires exactly one device (use -ip flag)")
			os.Exit(1)
		}

		device, err := client.Connect(deviceIPs[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
			os.Exit(1)
		}

		if *turnOn {
			if err := device.TurnOn(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to turn on: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Device %s turned ON\n", deviceIPs[0])
		} else {
			if err := device.TurnOff(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to turn off: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Device %s turned OFF\n", deviceIPs[0])
		}
		return
	}

	// Query each device
	allData := make(map[string]interface{})

	for i, deviceIP := range deviceIPs {
		if mode != modeJSON {
			if len(deviceIPs) > 1 {
				fmt.Printf("=== Device %d: %s ===\n", i+1, deviceIP)
			} else {
				fmt.Printf("Device: %s\n", deviceIP)
			}
		}

		device, err := client.Connect(deviceIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to connect to %s: %v\n", deviceIP, err)
			continue
		}

		data := queryDevice(device, mode, *rate, *currency)
		allData[deviceIP] = data

		if mode != modeJSON && len(deviceIPs) > 1 && i < len(deviceIPs)-1 {
			fmt.Println()
		}
	}

	// JSON output mode - output all data at the end
	if mode == modeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if len(deviceIPs) == 1 {
			enc.Encode(allData[deviceIPs[0]])
		} else {
			enc.Encode(allData)
		}
	}
}

func runDaemon(username, password, ip string, all bool, dbPath string, interval, timeout time.Duration) {
	if username == "" || password == "" {
		log.Fatal("Error: username and password required for daemon mode")
	}

	// Open database
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	log.Printf("Daemon starting with interval %v, database: %s", interval, dbPath)

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	client := tapo.NewClient(username, password)

	// Discover devices once at startup
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	var deviceIPs []string

	if ip != "" {
		deviceIPs = []string{ip}
	} else if all {
		devices, err := tapo.DiscoverWithTimeout(ctx, timeout)
		if err != nil {
			cancel()
			log.Fatalf("Discovery failed: %v", err)
		}
		for _, d := range devices {
			deviceIPs = append(deviceIPs, d.IP)
		}
	} else {
		device, err := tapo.DiscoverFirst(ctx)
		if err != nil {
			cancel()
			log.Fatalf("Discovery failed: %v", err)
		}
		deviceIPs = []string{device.IP}
	}
	cancel()

	if len(deviceIPs) == 0 {
		log.Fatal("No devices found")
	}

	log.Printf("Monitoring %d device(s): %v", len(deviceIPs), deviceIPs)

	// Initial poll
	pollDevices(client, db, deviceIPs)

	// Start ticker
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pollDevices(client, db, deviceIPs)
		case sig := <-sigChan:
			log.Printf("Received signal %v, shutting down...", sig)
			printDBStats(db)
			return
		}
	}
}

func pollDevices(client *tapo.Client, db *store.Store, deviceIPs []string) {
	now := time.Now()
	dateStr := now.Format("2006-01-02")

	for _, deviceIP := range deviceIPs {
		device, err := client.Connect(deviceIP)
		if err != nil {
			log.Printf("[%s] Connection failed: %v", deviceIP, err)
			continue
		}

		// Get device info for MAC
		info, err := device.GetDeviceInfo()
		mac := ""
		if err == nil && info != nil {
			mac = info.MAC
		}

		// Get and store current power
		power, err := device.GetCurrentPower()
		if err != nil {
			log.Printf("[%s] Failed to get power: %v", deviceIP, err)
		} else {
			if err := db.InsertReading(deviceIP, mac, power.CurrentPower); err != nil {
				log.Printf("[%s] Failed to store reading: %v", deviceIP, err)
			} else {
				log.Printf("[%s] Power: %.1f W", deviceIP, float64(power.CurrentPower)/1000.0)
			}
		}

		// Get and store hourly data
		hourly, err := device.GetEnergyData(tapo.EnergyDataHourly, now)
		if err != nil {
			log.Printf("[%s] Failed to get hourly data: %v", deviceIP, err)
		} else if hourly != nil {
			for hour, wh := range hourly.Data {
				if wh > 0 {
					if err := db.InsertHourly(dateStr, hour, deviceIP, wh); err != nil {
						log.Printf("[%s] Failed to store hourly: %v", deviceIP, err)
					}
				}
			}
		}

		// Get and store energy usage (for daily data)
		energyUsage, err := device.GetEnergyUsage()
		if err != nil {
			log.Printf("[%s] Failed to get energy usage: %v", deviceIP, err)
		} else if energyUsage != nil {
			if err := db.InsertDaily(dateStr, deviceIP, energyUsage.TodayEnergy, energyUsage.TodayRuntime); err != nil {
				log.Printf("[%s] Failed to store daily: %v", deviceIP, err)
			}
		}

		// Get and store monthly data
		monthly, err := device.GetEnergyData(tapo.EnergyDataMonthly, now)
		if err != nil {
			log.Printf("[%s] Failed to get monthly data: %v", deviceIP, err)
		} else if monthly != nil {
			year := now.Year()
			for month, wh := range monthly.Data {
				if wh > 0 {
					if err := db.InsertMonthly(year, month+1, deviceIP, wh); err != nil {
						log.Printf("[%s] Failed to store monthly: %v", deviceIP, err)
					}
				}
			}
		}
	}
}

func printDBStats(db *store.Store) {
	readings, hourly, daily, monthly, _ := db.GetStats()
	log.Printf("Database stats: %d readings, %d hourly, %d daily, %d monthly records",
		readings, hourly, daily, monthly)
}

func showHistory(dbPath, deviceIP string, days int, rate float64, currency string) {
	db, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get stats
	readings, hourlyCount, dailyCount, monthlyCount, _ := db.GetStats()
	fmt.Println("Database Statistics:")
	fmt.Printf("  Readings: %d | Hourly: %d | Daily: %d | Monthly: %d\n",
		readings, hourlyCount, dailyCount, monthlyCount)
	fmt.Println(strings.Repeat("─", 70))

	// Date range
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	// If no device specified, try to find one from the data
	if deviceIP == "" {
		// Get any device from daily records
		dailyRecords, _ := db.GetDailyRange("", startStr, endStr)
		if len(dailyRecords) > 0 {
			deviceIP = dailyRecords[0].DeviceIP
		}
	}

	fmt.Printf("Showing data for: %s (last %d days)\n", deviceIP, days)
	fmt.Println(strings.Repeat("─", 70))

	// Show recent readings (last 24 hours)
	fmt.Println("Recent Power Readings (last 24h):")
	recentReadings, err := db.GetReadingsRange(deviceIP, endDate.Add(-24*time.Hour), endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
	} else if len(recentReadings) == 0 {
		fmt.Println("  No readings found")
	} else {
		// Show summary and last few readings
		var totalPower int64
		var minPower, maxPower int = recentReadings[0].PowerMW, recentReadings[0].PowerMW
		for _, r := range recentReadings {
			totalPower += int64(r.PowerMW)
			if r.PowerMW < minPower {
				minPower = r.PowerMW
			}
			if r.PowerMW > maxPower {
				maxPower = r.PowerMW
			}
		}
		avgPower := float64(totalPower) / float64(len(recentReadings))
		fmt.Printf("  %d readings | Avg: %.1f W | Min: %.1f W | Max: %.1f W\n",
			len(recentReadings),
			avgPower/1000.0,
			float64(minPower)/1000.0,
			float64(maxPower)/1000.0)

		// Show last 10 readings
		fmt.Println("  Last 10 readings:")
		start := len(recentReadings) - 10
		if start < 0 {
			start = 0
		}
		for _, r := range recentReadings[start:] {
			fmt.Printf("    %s: %.1f W\n",
				r.Timestamp.Local().Format("15:04:05"),
				float64(r.PowerMW)/1000.0)
		}
	}

	fmt.Println(strings.Repeat("─", 70))

	// Show hourly data for today
	fmt.Println("Hourly Data (today):")
	todayStr := endDate.Format("2006-01-02")
	hourlyRecords, err := db.GetHourlyRange(deviceIP, todayStr, todayStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
	} else if len(hourlyRecords) == 0 {
		fmt.Println("  No hourly data found")
	} else {
		// Build hourly array
		hourlyData := make([]int, 24)
		for _, r := range hourlyRecords {
			if r.Hour >= 0 && r.Hour < 24 {
				hourlyData[r.Hour] = r.EnergyWh
			}
		}
		printHourlyTable(hourlyData)
	}

	fmt.Println(strings.Repeat("─", 70))

	// Show daily data
	fmt.Printf("Daily Data (last %d days):\n", days)
	dailyRecords, err := db.GetDailyRange(deviceIP, startStr, endStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
	} else if len(dailyRecords) == 0 {
		fmt.Println("  No daily data found")
	} else {
		var totalEnergy int
		fmt.Println("  Date        Energy    Runtime")
		for _, r := range dailyRecords {
			kwh := float64(r.EnergyWh) / 1000.0
			totalEnergy += r.EnergyWh
			cost := ""
			if rate > 0 {
				cost = fmt.Sprintf(" %s%.1f", currency, kwh*rate)
			}
			fmt.Printf("  %s  %6.2f kWh  %4dmin%s\n", r.Date, kwh, r.RuntimeMin, cost)
		}
		fmt.Printf("  Total: %.2f kWh", float64(totalEnergy)/1000.0)
		if rate > 0 {
			fmt.Printf(" (%s%.0f)", currency, float64(totalEnergy)/1000.0*rate)
		}
		fmt.Println()
	}

	fmt.Println(strings.Repeat("─", 70))

	// Show monthly data
	fmt.Println("Monthly Data (archived):")
	monthlyRecords, err := db.GetMonthlyRange(deviceIP, endDate.Year()-1, endDate.Year())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
	} else if len(monthlyRecords) == 0 {
		fmt.Println("  No monthly data found")
	} else {
		months := []string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
		var totalEnergy int
		for _, r := range monthlyRecords {
			kwh := float64(r.EnergyWh) / 1000.0
			totalEnergy += r.EnergyWh
			cost := ""
			if rate > 0 {
				cost = fmt.Sprintf(" %s%.0f", currency, kwh*rate)
			}
			fmt.Printf("  %d %s: %6.2f kWh%s\n", r.Year, months[r.Month], kwh, cost)
		}
		fmt.Printf("  Total archived: %.2f kWh", float64(totalEnergy)/1000.0)
		if rate > 0 {
			fmt.Printf(" (%s%.0f)", currency, float64(totalEnergy)/1000.0*rate)
		}
		fmt.Println()
	}
}

func queryDevice(device *tapo.P110, mode outputMode, rate float64, currency string) map[string]interface{} {
	data := make(map[string]interface{})

	// Device info
	info, err := device.GetDeviceInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get device info: %v\n", err)
	} else {
		data["device_info"] = info
	}

	// Device usage
	usage, err := device.GetDeviceUsage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get device usage: %v\n", err)
	} else {
		data["device_usage"] = usage
	}

	// Current power
	power, err := device.GetCurrentPower()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get current power: %v\n", err)
	} else {
		data["current_power"] = power
	}

	// Energy usage
	energyUsage, err := device.GetEnergyUsage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get energy usage: %v\n", err)
	} else {
		data["energy_usage"] = energyUsage
	}

	// Energy data
	today := time.Now()
	hourlyData, err := device.GetEnergyData(tapo.EnergyDataHourly, today)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get hourly energy data: %v\n", err)
	} else {
		data["energy_data_hourly"] = hourlyData
	}

	dailyData, err := device.GetEnergyData(tapo.EnergyDataDaily, today)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get daily energy data: %v\n", err)
	} else {
		data["energy_data_daily"] = dailyData
	}

	monthlyData, err := device.GetEnergyData(tapo.EnergyDataMonthly, today)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get monthly energy data: %v\n", err)
	} else {
		data["energy_data_monthly"] = monthlyData
	}

	// Output based on mode
	switch mode {
	case modeSummary:
		printSummary(info, usage, power, energyUsage, hourlyData, dailyData, monthlyData, rate, currency)
	case modeRaw:
		printRaw(data)
	}

	return data
}

func printSummary(info *tapo.DeviceInfo, usage *tapo.DeviceUsage, power *tapo.CurrentPower,
	energyUsage *tapo.EnergyUsage, hourly, daily, monthly *tapo.EnergyData, rate float64, currency string) {

	// Device header
	if info != nil {
		name := info.Nickname
		if name == "" {
			name = info.Model
		}
		status := "OFF"
		if info.DeviceON {
			status = "ON"
		}
		fmt.Printf("%s (%s) [%s] Signal: %ddBm\n", name, info.Model, status, info.RSSI)
	}
	fmt.Println(strings.Repeat("─", 70))

	// Current power and summary stats
	if power != nil {
		watts := float64(power.CurrentPower) / 1000.0
		fmt.Printf("Current: %.1f W", watts)
		if energyUsage != nil {
			fmt.Printf("   Today: %.3f kWh (%dmin)", float64(energyUsage.TodayEnergy)/1000.0, energyUsage.TodayRuntime)
			fmt.Printf("   Month: %.2f kWh", float64(energyUsage.MonthEnergy)/1000.0)
		}
		fmt.Println()

		if rate > 0 && energyUsage != nil {
			todayCost := float64(energyUsage.TodayEnergy) / 1000.0 * rate
			monthCost := float64(energyUsage.MonthEnergy) / 1000.0 * rate
			fmt.Printf("Cost:              Today: %s%.2f                    Month: %s%.2f\n", currency, todayCost, currency, monthCost)
		}
	}

	// Hourly - horizontal table
	if hourly != nil && len(hourly.Data) > 0 {
		fmt.Println(strings.Repeat("─", 70))
		printHourlyTable(hourly.Data)
	}

	// Daily - weekly summary rows
	if daily != nil && len(daily.Data) > 0 {
		fmt.Println(strings.Repeat("─", 70))
		printDailyWeekly(daily.Data)
	}

	// Monthly - horizontal bars
	if monthly != nil && len(monthly.Data) > 0 {
		fmt.Println(strings.Repeat("─", 70))
		printMonthlyBars(monthly.Data, rate, currency)
	}
}

func sum(data []int) int {
	total := 0
	for _, v := range data {
		total += v
	}
	return total
}

func maxVal(data []int) (int, int) {
	max := 0
	maxIdx := 0
	for i, v := range data {
		if v > max {
			max = v
			maxIdx = i
		}
	}
	return max, maxIdx
}

func printHourlyTable(data []int) {
	fmt.Println("Hourly (Wh):")

	// Row 1: Hours 0-11
	fmt.Print("  Hour: ")
	for i := 0; i < 12; i++ {
		fmt.Printf("%4d ", i)
	}
	fmt.Println()
	fmt.Print("    Wh: ")
	for i := 0; i < 12 && i < len(data); i++ {
		fmt.Printf("%4d ", data[i])
	}
	fmt.Println()

	// Row 2: Hours 12-23
	fmt.Print("  Hour: ")
	for i := 12; i < 24; i++ {
		fmt.Printf("%4d ", i)
	}
	fmt.Println()
	fmt.Print("    Wh: ")
	for i := 12; i < 24 && i < len(data); i++ {
		fmt.Printf("%4d ", data[i])
	}
	fmt.Println()

	// Summary
	total := sum(data)
	peak, peakHour := maxVal(data)
	fmt.Printf("  Total: %d Wh (%.3f kWh)  Peak: %d Wh @ %02d:00\n", total, float64(total)/1000.0, peak, peakHour)
}

func printDailyWeekly(data []int) {
	n := len(data)
	fmt.Printf("Daily (Wh) - %d days available:\n", n)

	// Show as weeks (7 days per row)
	fmt.Println("        Day:    1     2     3     4     5     6     7   Weekly")

	week := 1
	for i := 0; i < n; i += 7 {
		end := i + 7
		if end > n {
			end = n
		}

		weekData := data[i:end]
		weekTotal := sum(weekData)

		fmt.Printf("  Week %2d:  ", week)
		for _, v := range weekData {
			fmt.Printf("%5d ", v)
		}
		// Pad if incomplete week
		for j := len(weekData); j < 7; j++ {
			fmt.Print("    - ")
		}
		fmt.Printf(" %5d Wh (%.2f kWh)\n", weekTotal, float64(weekTotal)/1000.0)
		week++
	}

	// Grand totals
	total := sum(data)
	peak, peakDay := maxVal(data)
	avg := float64(total) / float64(n)
	fmt.Printf("  Total: %.2f kWh  Avg: %.0f Wh/day  Peak: %d Wh (day %d)\n",
		float64(total)/1000.0, avg, peak, peakDay+1)
}

func printMonthlyBars(data []int, rate float64, currency string) {
	months := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

	n := len(data)
	if n > 12 {
		n = 12
	}

	// Find max for bar scaling
	max := 1
	for i := 0; i < n; i++ {
		if data[i] > max {
			max = data[i]
		}
	}

	fmt.Println("Monthly:")
	for i := 0; i < n; i++ {
		kwh := float64(data[i]) / 1000.0
		barLen := 0
		if max > 0 {
			barLen = data[i] * 30 / max
		}
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", 30-barLen)

		if rate > 0 {
			cost := kwh * rate
			fmt.Printf("  %s %6.2f kWh %s %s%.0f\n", months[i], kwh, bar, currency, cost)
		} else {
			fmt.Printf("  %s %6.2f kWh %s\n", months[i], kwh, bar)
		}
	}

	// Year total
	total := sum(data[:n])
	fmt.Printf("  Year: %6.2f kWh", float64(total)/1000.0)
	if rate > 0 {
		fmt.Printf(" (%s%.0f)", currency, float64(total)/1000.0*rate)
	}
	fmt.Println()
}

func printRaw(data map[string]interface{}) {
	for key, val := range data {
		fmt.Printf("\n=== %s ===\n", key)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(val)
	}
}
