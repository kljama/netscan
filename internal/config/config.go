package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SNMPConfig holds SNMPv2c connection parameters
type SNMPConfig struct {
	Community string        `yaml:"community"`
	Port      int           `yaml:"port"`
	Timeout   time.Duration `yaml:"timeout"`
	Retries   int           `yaml:"retries"`
}

// InfluxDBConfig holds InfluxDB v2 connection parameters
type InfluxDBConfig struct {
	URL           string        `yaml:"url"`
	Token         string        `yaml:"token"`
	Org           string        `yaml:"org"`
	Bucket        string        `yaml:"bucket"`
	HealthBucket  string        `yaml:"health_bucket"`  // Bucket for health metrics
	BatchSize     int           `yaml:"batch_size"`     // Number of points to batch before writing
	BufferSize    int           `yaml:"buffer_size"`    // Buffer size for channel (drop points when full)
	FlushInterval time.Duration `yaml:"flush_interval"` // Maximum time to hold points before flushing
}

// Config holds all application configuration parameters
type Config struct {
	IcmpDiscoveryInterval   time.Duration  `yaml:"icmp_discovery_interval"`
	IcmpWorkers             int            `yaml:"icmp_workers"`
	SnmpWorkers             int            `yaml:"snmp_workers"`
	Networks                []string       `yaml:"networks"`
	SNMP                    SNMPConfig     `yaml:"snmp"`
	PingInterval            time.Duration  `yaml:"ping_interval"`
	PingTimeout             time.Duration  `yaml:"ping_timeout"`
	PingRateLimit           float64        `yaml:"ping_rate_limit"`            // Tokens per second (sustained ping rate)
	PingBurstLimit          int            `yaml:"ping_burst_limit"`           // Token bucket capacity (max burst)
	SNMPInterval            time.Duration  `yaml:"snmp_interval"`              // Interval for continuous SNMP polling per device
	SNMPRateLimit           float64        `yaml:"snmp_rate_limit"`            // Tokens per second (sustained SNMP query rate)
	SNMPBurstLimit          int            `yaml:"snmp_burst_limit"`           // Token bucket capacity (max SNMP burst)
	SNMPMaxConsecutiveFails int            `yaml:"snmp_max_consecutive_fails"` // Circuit breaker: max consecutive SNMP failures before suspension
	SNMPBackoffDuration     time.Duration  `yaml:"snmp_backoff_duration"`      // Circuit breaker: SNMP suspension duration after max failures
	InfluxDB                InfluxDBConfig `yaml:"influxdb"`
	HealthCheckPort         int            `yaml:"health_check_port"`      // HTTP health check endpoint port
	HealthReportInterval    time.Duration  `yaml:"health_report_interval"` // Interval for writing health metrics
	// Resource protection settings
	MaxConcurrentPingers     int           `yaml:"max_concurrent_pingers"`
	MaxConcurrentSNMPPollers int           `yaml:"max_concurrent_snmp_pollers"` // Maximum concurrent SNMP poller goroutines
	MaxDevices               int           `yaml:"max_devices"`
	MinScanInterval          time.Duration `yaml:"min_scan_interval"`
	MemoryLimitMB            int           `yaml:"memory_limit_mb"`
}

// LoadConfig parses YAML configuration file and returns Config struct
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Raw config struct for YAML parsing with string duration fields
	var raw struct {
		IcmpDiscoveryInterval   string     `yaml:"icmp_discovery_interval"`
		IcmpWorkers             int        `yaml:"icmp_workers"`
		SnmpWorkers             int        `yaml:"snmp_workers"`
		Networks                []string   `yaml:"networks"`
		SNMP                    SNMPConfig `yaml:"snmp"`
		PingInterval            string     `yaml:"ping_interval"`
		PingTimeout             string     `yaml:"ping_timeout"`
		PingRateLimit           float64    `yaml:"ping_rate_limit"`
		PingBurstLimit          int        `yaml:"ping_burst_limit"`
		SNMPInterval            string     `yaml:"snmp_interval"`
		SNMPRateLimit           float64    `yaml:"snmp_rate_limit"`
		SNMPBurstLimit          int        `yaml:"snmp_burst_limit"`
		SNMPMaxConsecutiveFails int        `yaml:"snmp_max_consecutive_fails"`
		SNMPBackoffDuration     string     `yaml:"snmp_backoff_duration"`
		InfluxDB                struct {
			URL           string `yaml:"url"`
			Token         string `yaml:"token"`
			Org           string `yaml:"org"`
			Bucket        string `yaml:"bucket"`
			HealthBucket  string `yaml:"health_bucket"`
			BatchSize     int    `yaml:"batch_size"`
			BufferSize    int    `yaml:"buffer_size"`
			FlushInterval string `yaml:"flush_interval"`
		} `yaml:"influxdb"`
		HealthCheckPort      int    `yaml:"health_check_port"`
		HealthReportInterval string `yaml:"health_report_interval"`
		// Resource protection settings
		MaxConcurrentPingers     int    `yaml:"max_concurrent_pingers"`
		MaxConcurrentSNMPPollers int    `yaml:"max_concurrent_snmp_pollers"`
		MaxDevices               int    `yaml:"max_devices"`
		MinScanInterval          string `yaml:"min_scan_interval"`
		MemoryLimitMB            int    `yaml:"memory_limit_mb"`
	}

	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}

	// Parse string durations to time.Duration
	icmpDiscoveryInterval, err := time.ParseDuration(raw.IcmpDiscoveryInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid icmp_discovery_interval: %v", err)
	}
	pingInterval, err := time.ParseDuration(raw.PingInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid ping_interval: %v", err)
	}

	// Parse ping_timeout with default if not specified
	var pingTimeout time.Duration
	if raw.PingTimeout != "" {
		pingTimeout, err = time.ParseDuration(raw.PingTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid ping_timeout: %v", err)
		}
	} else {
		// Default to 3s if not specified
		pingTimeout = 3 * time.Second
	}

	// Parse MinScanInterval if specified
	var minScanInterval time.Duration
	if raw.MinScanInterval != "" {
		minScanInterval, err = time.ParseDuration(raw.MinScanInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid min_scan_interval: %v", err)
		}
	}

	// Parse InfluxDB FlushInterval if specified
	var flushInterval time.Duration
	if raw.InfluxDB.FlushInterval != "" {
		flushInterval, err = time.ParseDuration(raw.InfluxDB.FlushInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid influxdb.flush_interval: %v", err)
		}
	}

	// Parse HealthReportInterval if specified
	var healthReportInterval time.Duration
	if raw.HealthReportInterval != "" {
		healthReportInterval, err = time.ParseDuration(raw.HealthReportInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid health_report_interval: %v", err)
		}
	}

	// Parse SNMPInterval if specified
	var snmpInterval time.Duration
	if raw.SNMPInterval != "" {
		snmpInterval, err = time.ParseDuration(raw.SNMPInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid snmp_interval: %v", err)
		}
	}

	// Parse SNMPBackoffDuration if specified
	var snmpBackoffDuration time.Duration
	if raw.SNMPBackoffDuration != "" {
		snmpBackoffDuration, err = time.ParseDuration(raw.SNMPBackoffDuration)
		if err != nil {
			return nil, fmt.Errorf("invalid snmp_backoff_duration: %v", err)
		}
	}

	// Set default SNMP timeout if not specified
	if raw.SNMP.Timeout == 0 {
		raw.SNMP.Timeout = 5 * time.Second
	}

	// Set default values if not specified
	if raw.IcmpWorkers == 0 {
		raw.IcmpWorkers = 64 // Default: 64 workers (reduced from 1024 to prevent resource contention)
	}
	if raw.SnmpWorkers == 0 {
		raw.SnmpWorkers = 32 // Default: 32 workers (reduced from 256 to match ICMP workers scale)
	}
	if raw.MaxConcurrentPingers == 0 {
		raw.MaxConcurrentPingers = 20000 // Default: allow up to 20,000 concurrent pingers
	}
	if raw.MaxDevices == 0 {
		raw.MaxDevices = 20000 // Default: allow up to 20,000 devices
	}
	if minScanInterval == 0 {
		minScanInterval = 1 * time.Minute // Default: minimum 1 minute between scans
	}
	if raw.MemoryLimitMB == 0 {
		raw.MemoryLimitMB = 16384 // Default: 16384MB memory limit
	}
	// Set InfluxDB batch defaults
	if raw.InfluxDB.BatchSize == 0 {
		raw.InfluxDB.BatchSize = 5000 // Default: batch 5000 points
	}
	if raw.InfluxDB.BufferSize == 0 {
		raw.InfluxDB.BufferSize = 100000 // Default: buffer 100000 points (~10s at 10k devices)
	}
	if flushInterval == 0 {
		flushInterval = 5 * time.Second // Default: flush every 5 seconds
	}
	// Set health bucket default
	if raw.InfluxDB.HealthBucket == "" {
		raw.InfluxDB.HealthBucket = "health" // Default: health bucket
	}
	// Set health report interval default
	if healthReportInterval == 0 {
		healthReportInterval = 10 * time.Second // Default: report health every 10 seconds
	}
	// Set health check port default
	if raw.HealthCheckPort == 0 {
		raw.HealthCheckPort = 8080 // Default: port 8080 for health checks
	}

	// Set ping rate limiting defaults
	if raw.PingRateLimit == 0 {
		raw.PingRateLimit = 64.0 // Default: 64 pings per second
	}
	if raw.PingBurstLimit == 0 {
		raw.PingBurstLimit = 256 // Default: allow bursts up to 256 pings
	}

	// Set SNMP continuous polling defaults
	if snmpInterval == 0 {
		snmpInterval = 1 * time.Hour // Default: poll SNMP every 1 hour per device
	}
	if raw.SNMPRateLimit == 0 {
		raw.SNMPRateLimit = 10.0 // Default: 10 SNMP queries per second
	}
	if raw.SNMPBurstLimit == 0 {
		raw.SNMPBurstLimit = 50 // Default: allow bursts up to 50 SNMP queries
	}
	if raw.SNMPMaxConsecutiveFails == 0 {
		raw.SNMPMaxConsecutiveFails = 5 // Default: 5 consecutive SNMP failures before suspension
	}
	if snmpBackoffDuration == 0 {
		snmpBackoffDuration = 1 * time.Hour // Default: 1 hour suspension for SNMP failures
	}
	if raw.MaxConcurrentSNMPPollers == 0 {
		raw.MaxConcurrentSNMPPollers = 20000 // Default: allow up to 20,000 concurrent SNMP pollers
	}

	// Apply environment variable expansion to sensitive fields
	raw.InfluxDB.URL = expandEnv(raw.InfluxDB.URL)
	raw.InfluxDB.Token = expandEnv(raw.InfluxDB.Token)
	raw.InfluxDB.Org = expandEnv(raw.InfluxDB.Org)
	raw.InfluxDB.Bucket = expandEnv(raw.InfluxDB.Bucket)
	raw.InfluxDB.HealthBucket = expandEnv(raw.InfluxDB.HealthBucket)
	raw.SNMP.Community = expandEnv(raw.SNMP.Community)

	return &Config{
		IcmpDiscoveryInterval:   icmpDiscoveryInterval,
		IcmpWorkers:             raw.IcmpWorkers,
		SnmpWorkers:             raw.SnmpWorkers,
		Networks:                raw.Networks,
		SNMP:                    raw.SNMP,
		PingInterval:            pingInterval,
		PingTimeout:             pingTimeout,
		PingRateLimit:           raw.PingRateLimit,
		PingBurstLimit:          raw.PingBurstLimit,
		SNMPInterval:            snmpInterval,
		SNMPRateLimit:           raw.SNMPRateLimit,
		SNMPBurstLimit:          raw.SNMPBurstLimit,
		SNMPMaxConsecutiveFails: raw.SNMPMaxConsecutiveFails,
		SNMPBackoffDuration:     snmpBackoffDuration,
		InfluxDB: InfluxDBConfig{
			URL:           raw.InfluxDB.URL,
			Token:         raw.InfluxDB.Token,
			Org:           raw.InfluxDB.Org,
			Bucket:        raw.InfluxDB.Bucket,
			HealthBucket:  raw.InfluxDB.HealthBucket,
			BatchSize:     raw.InfluxDB.BatchSize,
			BufferSize:    raw.InfluxDB.BufferSize,
			FlushInterval: flushInterval,
		},
		HealthCheckPort:          raw.HealthCheckPort,
		HealthReportInterval:     healthReportInterval,
		MaxConcurrentPingers:     raw.MaxConcurrentPingers,
		MaxConcurrentSNMPPollers: raw.MaxConcurrentSNMPPollers,
		MaxDevices:               raw.MaxDevices,
		MinScanInterval:          minScanInterval,
		MemoryLimitMB:            raw.MemoryLimitMB,
	}, nil
}

// expandEnv expands environment variables in a string, supporting ${VAR} and $VAR syntax
func expandEnv(s string) string {
	return os.ExpandEnv(s)
}

// ValidateConfig performs security and sanity checks on the configuration
// Returns warning message for security concerns, error for validation failures
func ValidateConfig(cfg *Config) (string, error) {
	// Validate network ranges
	for _, network := range cfg.Networks {
		if err := validateCIDR(network); err != nil {
			return "", err
		}
	}

	// Validate worker counts
	if cfg.IcmpWorkers < 1 || cfg.IcmpWorkers > 2000 {
		return "", fmt.Errorf("icmp_workers must be between 1 and 2000, got %d", cfg.IcmpWorkers)
	}
	if cfg.SnmpWorkers < 1 || cfg.SnmpWorkers > 1000 {
		return "", fmt.Errorf("snmp_workers must be between 1 and 1000, got %d", cfg.SnmpWorkers)
	}

	// Validate intervals
	if cfg.IcmpDiscoveryInterval < time.Minute {
		return "", fmt.Errorf("icmp_discovery_interval must be at least 1 minute, got %v", cfg.IcmpDiscoveryInterval)
	}
	if cfg.PingInterval < time.Second {
		return "", fmt.Errorf("ping_interval must be at least 1 second, got %v", cfg.PingInterval)
	}

	// Validate SNMP settings
	if cfg.SNMP.Port < 1 || cfg.SNMP.Port > 65535 {
		return "", fmt.Errorf("snmp port must be between 1 and 65535, got %d", cfg.SNMP.Port)
	}
	if cfg.SNMP.Timeout < time.Second {
		return "", fmt.Errorf("snmp timeout must be at least 1 second, got %v", cfg.SNMP.Timeout)
	}
	if cfg.SNMP.Retries < 0 || cfg.SNMP.Retries > 10 {
		return "", fmt.Errorf("snmp retries must be between 0 and 10, got %d", cfg.SNMP.Retries)
	}

	// Validate and sanitize SNMP community string
	if warning, err := validateSNMPCommunity(cfg.SNMP.Community); err != nil {
		return "", err
	} else if warning != "" {
		// Return the warning
		return warning, nil
	}

	// Validate required fields
	if cfg.InfluxDB.URL == "" {
		return "", fmt.Errorf("influxdb.url is required")
	}
	if err := validateURL(cfg.InfluxDB.URL); err != nil {
		return "", fmt.Errorf("influxdb.url validation failed: %v", err)
	}
	if cfg.InfluxDB.Token == "" {
		return "", fmt.Errorf("influxdb.token is required")
	}
	if cfg.InfluxDB.Org == "" {
		return "", fmt.Errorf("influxdb.org is required")
	}
	if cfg.InfluxDB.Bucket == "" {
		return "", fmt.Errorf("influxdb.bucket is required")
	}
	if cfg.SNMP.Community == "" {
		return "", fmt.Errorf("snmp.community is required")
	}

	// Validate network ranges contain valid IP addresses
	for _, network := range cfg.Networks {
		if err := validateNetworkContainsValidIPs(network); err != nil {
			return "", fmt.Errorf("network validation failed for %s: %v", network, err)
		}
	}

	// Validate resource protection settings
	if cfg.MaxConcurrentPingers < 1 || cfg.MaxConcurrentPingers > 100000 {
		return "", fmt.Errorf("max_concurrent_pingers must be between 1 and 100000, got %d", cfg.MaxConcurrentPingers)
	}
	if cfg.MaxConcurrentSNMPPollers < 1 || cfg.MaxConcurrentSNMPPollers > 100000 {
		return "", fmt.Errorf("max_concurrent_snmp_pollers must be between 1 and 100000, got %d", cfg.MaxConcurrentSNMPPollers)
	}
	if cfg.MaxDevices < 1 || cfg.MaxDevices > 100000 {
		return "", fmt.Errorf("max_devices must be between 1 and 100000, got %d", cfg.MaxDevices)
	}
	if cfg.MinScanInterval < 30*time.Second {
		return "", fmt.Errorf("min_scan_interval must be at least 30 seconds, got %v", cfg.MinScanInterval)
	}
	if cfg.MemoryLimitMB < 64 || cfg.MemoryLimitMB > 16384 {
		return "", fmt.Errorf("memory_limit_mb must be between 64 and 16384, got %d", cfg.MemoryLimitMB)
	}

	// Validate ping rate limiting settings
	if cfg.PingRateLimit <= 0 {
		return "", fmt.Errorf("ping_rate_limit must be greater than 0, got %.2f", cfg.PingRateLimit)
	}
	if cfg.PingBurstLimit <= 0 {
		return "", fmt.Errorf("ping_burst_limit must be greater than 0, got %d", cfg.PingBurstLimit)
	}
	// Burst should be at least equal to rate to avoid immediate throttling
	if float64(cfg.PingBurstLimit) < cfg.PingRateLimit {
		return "WARNING: ping_burst_limit should be >= ping_rate_limit to avoid immediate throttling", nil
	}

	// Validate SNMP continuous polling settings
	if cfg.SNMPInterval < time.Minute {
		return "", fmt.Errorf("snmp_interval must be at least 1 minute, got %v", cfg.SNMPInterval)
	}
	if cfg.SNMPRateLimit <= 0 {
		return "", fmt.Errorf("snmp_rate_limit must be greater than 0, got %.2f", cfg.SNMPRateLimit)
	}
	if cfg.SNMPBurstLimit <= 0 {
		return "", fmt.Errorf("snmp_burst_limit must be greater than 0, got %d", cfg.SNMPBurstLimit)
	}
	// Burst should be at least equal to rate to avoid immediate throttling
	if float64(cfg.SNMPBurstLimit) < cfg.SNMPRateLimit {
		return "WARNING: snmp_burst_limit should be >= snmp_rate_limit to avoid immediate throttling", nil
	}
	if cfg.SNMPMaxConsecutiveFails <= 0 {
		return "", fmt.Errorf("snmp_max_consecutive_fails must be greater than 0, got %d", cfg.SNMPMaxConsecutiveFails)
	}
	if cfg.SNMPBackoffDuration < time.Minute {
		return "", fmt.Errorf("snmp_backoff_duration must be at least 1 minute, got %v", cfg.SNMPBackoffDuration)
	}

	return "", nil
}

// validateCIDR validates a CIDR notation and checks for dangerous network ranges
func validateCIDR(cidr string) error {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR notation: %s", cidr)
	}

	// Check for dangerous network ranges
	networkIP := network.IP
	if networkIP.IsLoopback() {
		return fmt.Errorf("loopback networks not allowed: %s", cidr)
	}
	if networkIP.IsMulticast() {
		return fmt.Errorf("multicast networks not allowed: %s", cidr)
	}
	if networkIP.IsLinkLocalUnicast() {
		return fmt.Errorf("link-local networks not allowed: %s", cidr)
	}

	// Check for overly broad ranges (larger than /8)
	ones, _ := network.Mask.Size()
	if ones < 8 {
		return fmt.Errorf("network range too broad (/%d), maximum allowed is /8: %s", ones, cidr)
	}

	return nil
}

// validateSNMPCommunity validates and sanitizes SNMP community strings
// validateSNMPCommunity validates and sanitizes SNMP community string
// Returns warning message for security concerns, error for validation failures
func validateSNMPCommunity(community string) (string, error) {
	if len(community) == 0 {
		return "", fmt.Errorf("snmp community string cannot be empty")
	}

	if len(community) > 32 {
		return "", fmt.Errorf("snmp community string too long (max 32 characters), got %d characters", len(community))
	}

	// Check for potentially dangerous characters
	for _, char := range community {
		// Allow alphanumeric, hyphen, underscore, and dot
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.') {
			return "", fmt.Errorf("snmp community string contains invalid character: %c", char)
		}
	}

	// Check for common default/weak community strings
	weakCommunities := []string{"private", "admin", "password", "123456", "community"}
	for _, weak := range weakCommunities {
		if community == weak {
			return "", fmt.Errorf("snmp community string '%s' is a common default value and should be changed for security", community)
		}
	}

	// Allow "public" but issue a warning
	if community == "public" {
		return "WARNING: Using default SNMP community 'public' - consider changing for security", nil
	}

	return "", nil
}

// validateURL validates URL format and scheme for InfluxDB
func validateURL(urlStr string) error {
	if len(urlStr) == 0 {
		return fmt.Errorf("URL cannot be empty")
	}

	if len(urlStr) > 2048 {
		return fmt.Errorf("URL too long (max 2048 characters)")
	}

	// Basic URL validation - check for http/https scheme
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return fmt.Errorf("URL must use http or https scheme")
	}

	// Parse URL to validate format
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %v", err)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must include a valid host")
	}

	// Check for localhost/loopback in production-like environments
	// Allow localhost for development/testing but warn
	if strings.Contains(parsedURL.Host, "localhost") || strings.Contains(parsedURL.Host, "127.0.0.1") {
		// This is allowed but we could add a warning in the future
		// For now, just continue - the user may be using docker-compose for testing
	}

	return nil
}

// validateTimeFormat validates time in HH:MM format (24-hour)
func validateTimeFormat(timeStr string) error {
	if len(timeStr) != 5 {
		return fmt.Errorf("time must be in HH:MM format, got %s", timeStr)
	}

	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("time must be in HH:MM format, got %s", timeStr)
	}

	// Parse hours
	var hour, minute int
	_, err := fmt.Sscanf(timeStr, "%02d:%02d", &hour, &minute)
	if err != nil {
		return fmt.Errorf("invalid time format %s: %v", timeStr, err)
	}

	if hour < 0 || hour > 23 {
		return fmt.Errorf("hour must be between 00 and 23, got %d", hour)
	}
	if minute < 0 || minute > 59 {
		return fmt.Errorf("minute must be between 00 and 59, got %d", minute)
	}

	return nil
}

// validateNetworkContainsValidIPs validates that a CIDR network range contains valid IP addresses
func validateNetworkContainsValidIPs(cidr string) error {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %v", err)
	}

	// Check if the network IP is valid
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("network IP is unspecified")
	}

	// Get the first and last IP in the range
	firstIP := network.IP
	lastIP := make(net.IP, len(firstIP))
	copy(lastIP, firstIP)

	// Calculate the last IP by ORing with the inverted mask
	for i := range lastIP {
		lastIP[i] |= ^network.Mask[i]
	}

	// Validate first IP
	if !firstIP.IsGlobalUnicast() && !firstIP.IsPrivate() {
		return fmt.Errorf("first IP %s is not a valid unicast address", firstIP)
	}

	// Validate last IP
	if !lastIP.IsGlobalUnicast() && !lastIP.IsPrivate() {
		return fmt.Errorf("last IP %s is not a valid unicast address", lastIP)
	}

	// Check for unreasonably large ranges that could cause resource exhaustion
	ones, bits := network.Mask.Size()
	hostBits := bits - ones
	if hostBits > 24 { // More than 16M addresses
		return fmt.Errorf("network range too large (/%d = 2^%d addresses), maximum allowed is /8", ones, hostBits)
	}

	return nil
}
