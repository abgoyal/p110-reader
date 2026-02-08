package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store handles SQLite database operations for energy data.
type Store struct {
	db *sql.DB
}

// Reading represents a power reading snapshot.
type Reading struct {
	ID        int64
	Timestamp time.Time
	DeviceIP  string
	DeviceMAC string
	PowerMW   int // milliwatts
}

// HourlyRecord represents archived hourly energy data.
type HourlyRecord struct {
	ID       int64
	Date     string // YYYY-MM-DD
	Hour     int
	DeviceIP string
	EnergyWh int
}

// DailyRecord represents archived daily energy data.
type DailyRecord struct {
	ID         int64
	Date       string // YYYY-MM-DD
	DeviceIP   string
	EnergyWh   int
	RuntimeMin int
}

// MonthlyRecord represents archived monthly energy data.
type MonthlyRecord struct {
	ID       int64
	Year     int
	Month    int
	DeviceIP string
	EnergyWh int
}

// Open opens or creates a SQLite database at the given path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return store, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// init creates the database schema if it doesn't exist.
func (s *Store) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		device_ip TEXT NOT NULL,
		device_mac TEXT,
		power_mw INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_readings_ts ON readings(timestamp);
	CREATE INDEX IF NOT EXISTS idx_readings_device ON readings(device_ip);

	CREATE TABLE IF NOT EXISTS hourly (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		hour INTEGER NOT NULL,
		device_ip TEXT NOT NULL,
		energy_wh INTEGER NOT NULL,
		UNIQUE(date, hour, device_ip)
	);
	CREATE INDEX IF NOT EXISTS idx_hourly_date ON hourly(date);

	CREATE TABLE IF NOT EXISTS daily (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		device_ip TEXT NOT NULL,
		energy_wh INTEGER NOT NULL,
		runtime_min INTEGER,
		UNIQUE(date, device_ip)
	);
	CREATE INDEX IF NOT EXISTS idx_daily_date ON daily(date);

	CREATE TABLE IF NOT EXISTS monthly (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		year INTEGER NOT NULL,
		month INTEGER NOT NULL,
		device_ip TEXT NOT NULL,
		energy_wh INTEGER NOT NULL,
		UNIQUE(year, month, device_ip)
	);
	CREATE INDEX IF NOT EXISTS idx_monthly_ym ON monthly(year, month);
	`

	_, err := s.db.Exec(schema)
	return err
}

// InsertReading stores a power reading snapshot.
func (s *Store) InsertReading(deviceIP, deviceMAC string, powerMW int) error {
	_, err := s.db.Exec(
		"INSERT INTO readings (timestamp, device_ip, device_mac, power_mw) VALUES (?, ?, ?, ?)",
		time.Now().UTC(), deviceIP, deviceMAC, powerMW,
	)
	return err
}

// InsertHourly stores or updates hourly energy data.
func (s *Store) InsertHourly(date string, hour int, deviceIP string, energyWh int) error {
	_, err := s.db.Exec(
		`INSERT INTO hourly (date, hour, device_ip, energy_wh) VALUES (?, ?, ?, ?)
		 ON CONFLICT(date, hour, device_ip) DO UPDATE SET energy_wh = excluded.energy_wh`,
		date, hour, deviceIP, energyWh,
	)
	return err
}

// InsertDaily stores or updates daily energy data.
func (s *Store) InsertDaily(date string, deviceIP string, energyWh int, runtimeMin int) error {
	_, err := s.db.Exec(
		`INSERT INTO daily (date, device_ip, energy_wh, runtime_min) VALUES (?, ?, ?, ?)
		 ON CONFLICT(date, device_ip) DO UPDATE SET energy_wh = excluded.energy_wh, runtime_min = excluded.runtime_min`,
		date, deviceIP, energyWh, runtimeMin,
	)
	return err
}

// InsertMonthly stores or updates monthly energy data.
func (s *Store) InsertMonthly(year, month int, deviceIP string, energyWh int) error {
	_, err := s.db.Exec(
		`INSERT INTO monthly (year, month, device_ip, energy_wh) VALUES (?, ?, ?, ?)
		 ON CONFLICT(year, month, device_ip) DO UPDATE SET energy_wh = excluded.energy_wh`,
		year, month, deviceIP, energyWh,
	)
	return err
}

// GetLatestReading returns the most recent reading for a device.
func (s *Store) GetLatestReading(deviceIP string) (*Reading, error) {
	row := s.db.QueryRow(
		"SELECT id, timestamp, device_ip, device_mac, power_mw FROM readings WHERE device_ip = ? ORDER BY timestamp DESC LIMIT 1",
		deviceIP,
	)

	var r Reading
	var ts string
	err := row.Scan(&r.ID, &ts, &r.DeviceIP, &r.DeviceMAC, &r.PowerMW)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	r.Timestamp, _ = time.Parse(time.RFC3339, ts)
	return &r, nil
}

// GetReadingsRange returns readings within a time range.
func (s *Store) GetReadingsRange(deviceIP string, start, end time.Time) ([]Reading, error) {
	rows, err := s.db.Query(
		"SELECT id, timestamp, device_ip, device_mac, power_mw FROM readings WHERE device_ip = ? AND timestamp >= ? AND timestamp <= ? ORDER BY timestamp",
		deviceIP, start.UTC(), end.UTC(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var readings []Reading
	for rows.Next() {
		var r Reading
		var ts string
		if err := rows.Scan(&r.ID, &ts, &r.DeviceIP, &r.DeviceMAC, &r.PowerMW); err != nil {
			return nil, err
		}
		r.Timestamp, _ = time.Parse(time.RFC3339, ts)
		readings = append(readings, r)
	}
	return readings, rows.Err()
}

// GetHourlyRange returns hourly records within a date range.
func (s *Store) GetHourlyRange(deviceIP string, startDate, endDate string) ([]HourlyRecord, error) {
	rows, err := s.db.Query(
		"SELECT id, date, hour, device_ip, energy_wh FROM hourly WHERE device_ip = ? AND date >= ? AND date <= ? ORDER BY date, hour",
		deviceIP, startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []HourlyRecord
	for rows.Next() {
		var r HourlyRecord
		if err := rows.Scan(&r.ID, &r.Date, &r.Hour, &r.DeviceIP, &r.EnergyWh); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// GetDailyRange returns daily records within a date range.
// If deviceIP is empty, returns records for all devices.
func (s *Store) GetDailyRange(deviceIP string, startDate, endDate string) ([]DailyRecord, error) {
	var rows *sql.Rows
	var err error

	if deviceIP == "" {
		rows, err = s.db.Query(
			"SELECT id, date, device_ip, energy_wh, runtime_min FROM daily WHERE date >= ? AND date <= ? ORDER BY date",
			startDate, endDate,
		)
	} else {
		rows, err = s.db.Query(
			"SELECT id, date, device_ip, energy_wh, runtime_min FROM daily WHERE device_ip = ? AND date >= ? AND date <= ? ORDER BY date",
			deviceIP, startDate, endDate,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DailyRecord
	for rows.Next() {
		var r DailyRecord
		if err := rows.Scan(&r.ID, &r.Date, &r.DeviceIP, &r.EnergyWh, &r.RuntimeMin); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// GetMonthlyRange returns monthly records within a year range.
func (s *Store) GetMonthlyRange(deviceIP string, startYear, endYear int) ([]MonthlyRecord, error) {
	rows, err := s.db.Query(
		"SELECT id, year, month, device_ip, energy_wh FROM monthly WHERE device_ip = ? AND year >= ? AND year <= ? ORDER BY year, month",
		deviceIP, startYear, endYear,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MonthlyRecord
	for rows.Next() {
		var r MonthlyRecord
		if err := rows.Scan(&r.ID, &r.Year, &r.Month, &r.DeviceIP, &r.EnergyWh); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// GetStats returns database statistics.
func (s *Store) GetStats() (readings, hourly, daily, monthly int64, err error) {
	s.db.QueryRow("SELECT COUNT(*) FROM readings").Scan(&readings)
	s.db.QueryRow("SELECT COUNT(*) FROM hourly").Scan(&hourly)
	s.db.QueryRow("SELECT COUNT(*) FROM daily").Scan(&daily)
	s.db.QueryRow("SELECT COUNT(*) FROM monthly").Scan(&monthly)
	return
}
