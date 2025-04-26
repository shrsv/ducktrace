package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
	_ "github.com/marcboeker/go-duckdb"
)

type Config struct {
	LogFormat struct {
		Pattern string
	}
	LogLevel string `toml:"log_level"`
	Events   map[string]struct {
		StartRegex string `toml:"start_regex"`
		EndRegex   string `toml:"end_regex"`
	}
}

var logger *log.Logger
var debugEnabled bool

func main() {
	// Setup logger
	logger = log.New(os.Stderr, "[ducktrace] ", log.LstdFlags|log.Lshortfile)
	logger.Println("Starting ducktrace...")

	db, err := sql.Open("duckdb", ":memory:")
	must(err)
	defer db.Close()
	logger.Println("Opened in-memory DuckDB database")

	must(exec(db, `CREATE TABLE logs (timestamp TIMESTAMP, level TEXT, message TEXT)`))
	logger.Println("Created logs table")

	config := loadConfig("config.toml")
	logger.Printf("Loaded config: %+v\n", config)
	if config.LogFormat.Pattern == "" {
		logger.Printf("Config error: LogFormat.Pattern is empty. Please check your config.toml.\n")
		os.Exit(1)
	}
	if config.LogLevel == "debug" {
		debugEnabled = true
		logger.Println("Debug logging enabled")
	}
	lineRegex := regexp.MustCompile(config.LogFormat.Pattern)
	logger.Printf("Compiled log line regex: %s\n", config.LogFormat.Pattern)

	file, err := os.Open("sample.log")
	must(err)
	defer file.Close()
	logger.Println("Opened sample.log for reading")

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if debugEnabled {
			logger.Printf("Read line: %s\n", line)
		}
		matches := lineRegex.FindStringSubmatch(line)
		if matches == nil {
			if debugEnabled {
				logger.Printf("Line did not match regex: %s\n", line)
			}
			continue
		}
		if len(matches) < 5 {
			logger.Printf("Regex match error: expected at least 5 groups, got %d for line: %s\n", len(matches), line)
			continue
		}
		ts := parseTimestamp(matches[1], matches[2])
		level := matches[3]
		message := matches[4]
		if debugEnabled {
			logger.Printf("Parsed log entry: ts=%v, level=%s, message=%s\n", ts, level, message)
		}
		must(exec(db, `INSERT INTO logs (timestamp, level, message) VALUES (?, ?, ?)`, ts, level, message))
	}
	must(scanner.Err())
	logger.Println("Finished reading and inserting log lines")

	for name, event := range config.Events {
		logger.Printf("Analyzing event: %s\n", name)
		analyzeEvent(db, name, event.StartRegex, event.EndRegex)
	}
}

func loadConfig(path string) Config {
	var cfg Config
	logger.Printf("Loading config from %s\n", path)
	_, err := toml.DecodeFile(path, &cfg)
	must(err)
	return cfg
}

func parseTimestamp(dateStr, timeStr string) time.Time {
	ts, err := time.Parse("2006-01-02 15:04:05", dateStr+" "+timeStr)
	if debugEnabled {
		logger.Printf("Parsing timestamp: %s %s\n", dateStr, timeStr)
	}
	must(err)
	return ts
}

func exec(db *sql.DB, query string, args ...interface{}) error {
	if debugEnabled {
		logger.Printf("Executing SQL: %s, args=%v\n", query, args)
	}
	_, err := db.Exec(query, args...)
	if err != nil {
		logger.Printf("SQL error: %v\n", err)
	}
	return err
}

func analyzeEvent(db *sql.DB, name, startRegex, endRegex string) {
	logger.Printf("Analyzing event: %s, startRegex=%s, endRegex=%s\n", name, startRegex, endRegex)
	fmt.Printf("\n=== %s ===\n", name)

	rows, err := db.Query(`
        SELECT timestamp, message
        FROM logs
        ORDER BY timestamp
    `)
	must(err)
	defer rows.Close()

	startR := regexp.MustCompile(startRegex)
	endR := regexp.MustCompile(endRegex)

	var starts []time.Time
	var ends []time.Time

	for rows.Next() {
		var ts time.Time
		var msg string
		must(rows.Scan(&ts, &msg))

		if startR.MatchString(msg) {
			if debugEnabled {
				logger.Printf("Matched start for event %s at %v: %s\n", name, ts, msg)
			}
			starts = append(starts, ts)
		}
		if endR.MatchString(msg) {
			if debugEnabled {
				logger.Printf("Matched end for event %s at %v: %s\n", name, ts, msg)
			}
			ends = append(ends, ts)
		}
	}

	if len(starts) == 0 || len(ends) == 0 {
		logger.Printf("No matches for event %s: starts=%d, ends=%d\n", name, len(starts), len(ends))
		fmt.Println("No matches.")
		return
	}

	minLen := len(starts)
	if len(ends) < minLen {
		minLen = len(ends)
	}

	var totalDuration time.Duration
	for i := 0; i < minLen; i++ {
		d := ends[i].Sub(starts[i])
		totalDuration += d
		fmt.Printf("Instance %d: %v\n", i+1, d)
		if debugEnabled {
			logger.Printf("Event %s instance %d duration: %v\n", name, i+1, d)
		}
	}

	avg := totalDuration / time.Duration(minLen)
	fmt.Printf("Average Duration: %v\n", avg)
	if debugEnabled {
		logger.Printf("Event %s average duration: %v\n", name, avg)
	}
}

func must(err error) {
	if err != nil {
		logger.Printf("Fatal error: %v\n", err)
		panic(err)
	}
}
