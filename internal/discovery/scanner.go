package discovery

import (
	"context"
	"encoding/binary"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/kljama/netscan/internal/config"
	"github.com/kljama/netscan/internal/snmp"
	"github.com/kljama/netscan/internal/state"
	probing "github.com/prometheus-community/pro-bing"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// RunScanIPsOnly returns all IP addresses in the specified CIDR range
func RunScanIPsOnly(cidr string) []string {
	return ipsFromCIDR(cidr)
}

// RunICMPSweep performs concurrent ICMP ping sweep across multiple networks
// Returns only the IP addresses that responded to pings
// The limiter parameter controls the global rate of ping operations
// The ctx parameter enables graceful shutdown and rate limiter cancellation
func RunICMPSweep(ctx context.Context, networks []string, workers int, limiter *rate.Limiter) []string {
	if workers <= 0 {
		workers = 64 // Default
	}

	var (
		jobs    = make(chan string, 256)
		results = make(chan string, 256)
		wg      sync.WaitGroup
	)

	// Worker goroutine for ICMP ping probes
	worker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("ICMP worker panic recovered")
			}
		}()

		defer wg.Done()
		for ip := range jobs {
			// Acquire token from rate limiter before pinging
			// This ensures discovery scans respect the global ping rate limit
			if limiter != nil {
				if err := limiter.Wait(ctx); err != nil {
					// Context was cancelled while waiting for token
					log.Debug().
						Str("ip", ip).
						Msg("ICMP discovery cancelled while waiting for rate limit token")
					return
				}
			}

			pinger, err := probing.NewPinger(ip)
			if err != nil {
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("Failed to create pinger")
				continue // Skip invalid IP addresses
			}
			pinger.Count = 1                 // Single ping per device
			pinger.Timeout = 1 * time.Second // 1-second discovery timeout
			pinger.SetPrivileged(true)       // Use raw sockets for ICMP
			if err := pinger.Run(); err != nil {
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("Ping failed")
				continue // Skip ping failures
			}
			stats := pinger.Statistics()
			if stats.PacketsRecv > 0 { // Device responded to ping
				results <- ip
			}
		}
	}

	// Launch concurrent ping worker goroutines
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer: collect all IPs, shuffle them, then enqueue in randomized order
	go func() {
		// Panic recovery for producer goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("ICMP producer panic recovered")
			}
			close(jobs)
		}()

		// Shuffle networks to randomize which network we scan first
		shuffledNetworks := make([]string, len(networks))
		copy(shuffledNetworks, networks)
		rand.Shuffle(len(shuffledNetworks), func(i, j int) {
			shuffledNetworks[i], shuffledNetworks[j] = shuffledNetworks[j], shuffledNetworks[i]
		})

		for _, network := range shuffledNetworks {
			// Check context
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Stream IPs in random order
			streamRandomizedNetwork(ctx, network, jobs)
		}
	}()

	// Wait for all workers to complete, then close results channel
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("ICMP wait goroutine panic recovered")
			}
		}()

		wg.Wait()
		close(results)
	}()

	// Collect all responsive IPs
	var responsiveIPs []string
	for ip := range results {
		responsiveIPs = append(responsiveIPs, ip)
	}
	return responsiveIPs
}

// RunSNMPScan performs concurrent SNMP queries on a list of IP addresses
// Returns devices with SNMP data populated, gracefully handles SNMP failures
func RunSNMPScan(ips []string, snmpConfig *config.SNMPConfig, workers int) []state.Device {
	if workers <= 0 {
		workers = 32 // Default
	}

	var (
		jobs    = make(chan string, 256)
		results = make(chan state.Device, 256)
		wg      sync.WaitGroup
	)

	// Worker goroutine for SNMP queries
	worker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("SNMP worker panic recovered")
			}
		}()

		defer wg.Done()
		for ip := range jobs {
			// Configure SNMP connection parameters
			params := &gosnmp.GoSNMP{
				Target:    ip,
				Port:      uint16(snmpConfig.Port),
				Community: snmpConfig.Community,
				Version:   gosnmp.Version2c,
				Timeout:   snmpConfig.Timeout,
				Retries:   snmpConfig.Retries,
			}
			if err := params.Connect(); err != nil {
				// SNMP failed, skip this device
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("SNMP connection failed")
				continue
			}
			// Query standard MIB-II system OIDs: sysName, sysDescr
			oids := []string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0"}
			resp, err := snmp.GetWithFallback(params, oids)
			params.Conn.Close()
			if err != nil || len(resp.Variables) < 2 {
				// SNMP query failed, skip this device
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("SNMP query failed")
				continue
			}

			// Validate and sanitize SNMP response data
			hostname, err := snmp.ValidateString(resp.Variables[0].Value, "sysName")
			if err != nil {
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("Invalid sysName")
				continue
			}
			sysDescr, err := snmp.ValidateString(resp.Variables[1].Value, "sysDescr")
			if err != nil {
				log.Debug().
					Str("ip", ip).
					Err(err).
					Msg("Invalid sysDescr")
				continue
			}

			dev := state.Device{
				IP:       ip,
				Hostname: hostname,
				SysDescr: sysDescr,
				LastSeen: time.Now(),
			}
			results <- dev
		}
	}

	// Launch concurrent SNMP worker goroutines
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer: enqueue all IPs
	go func() {
		// Panic recovery for producer goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("SNMP producer panic recovered")
			}
		}()

		for _, ip := range ips {
			jobs <- ip
		}
		close(jobs)
	}()

	// Wait for all workers to complete, then close results channel
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("SNMP wait goroutine panic recovered")
			}
		}()

		wg.Wait()
		close(results)
	}()

	// Collect all discovered devices
	var devices []state.Device
	for dev := range results {
		devices = append(devices, dev)
	}
	return devices
}

// RunScan performs concurrent SNMPv2c discovery across configured networks
func RunScan(cfg *config.Config) []state.Device {
	var (
		jobs    = make(chan string, 256)       // Buffered channel for IP addresses to scan
		results = make(chan state.Device, 256) // Buffered channel for discovered devices
		wg      sync.WaitGroup
	)

	// Worker goroutine for SNMP queries
	worker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunScan SNMP worker panic recovered")
			}
		}()

		defer wg.Done()
		for ip := range jobs {
			// Configure SNMP connection parameters
			params := &gosnmp.GoSNMP{
				Target:    ip,
				Port:      uint16(cfg.SNMP.Port),
				Community: cfg.SNMP.Community,
				Version:   gosnmp.Version2c,
				Timeout:   cfg.SNMP.Timeout,
				Retries:   cfg.SNMP.Retries,
			}
			if err := params.Connect(); err != nil {
				continue // Skip unresponsive devices
			}
			// Query standard MIB-II system OIDs: sysName, sysDescr
			oids := []string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0"}
			resp, err := snmp.GetWithFallback(params, oids)
			params.Conn.Close()
			if err != nil || len(resp.Variables) < 2 {
				continue // Skip devices with incomplete SNMP responses
			}

			// Validate and sanitize SNMP response data
			hostname, err := snmp.ValidateString(resp.Variables[0].Value, "sysName")
			if err != nil {
				continue // Skip devices with invalid hostname data
			}
			sysDescr, err := snmp.ValidateString(resp.Variables[1].Value, "sysDescr")
			if err != nil {
				continue // Skip devices with invalid description data
			}

			dev := state.Device{
				IP:       ip,
				Hostname: hostname,
				SysDescr: sysDescr,
				LastSeen: time.Now(),
			}
			results <- dev
		}
	}

	// Launch concurrent SNMP worker goroutines
	workerCount := cfg.SnmpWorkers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer: enqueue all IPs from configured CIDR ranges
	for _, cidr := range cfg.Networks {
		// Stream IPs directly to jobs channel without intermediate array
		streamIPsFromCIDR(cidr, jobs)
	}
	close(jobs)

	// Wait for all workers to complete, then close results channel
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunScan wait goroutine panic recovered")
			}
		}()

		wg.Wait()
		close(results)
	}()

	// Collect all discovered devices
	var found []state.Device
	for dev := range results {
		found = append(found, dev)
	}
	return found
}

// RunPingDiscovery performs concurrent ICMP ping sweep to find online devices
func RunPingDiscovery(cidr string, icmpWorkers int) []state.Device {
	// Calculate buffer size based on network size, capped at reasonable limit
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Error().
			Str("cidr", cidr).
			Err(err).
			Msg("Invalid CIDR")
		return []state.Device{}
	}

	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	bufferSize := 256 // Default buffer
	if hostBits < 8 {
		bufferSize = 1 << hostBits // Smaller networks can use exact size
	}

	var (
		jobs    = make(chan string, bufferSize)       // Buffered channel for IP addresses to ping
		results = make(chan state.Device, bufferSize) // Buffered channel for responsive devices
		wg      sync.WaitGroup
	)

	// Worker goroutine for ICMP ping probes
	worker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunPingDiscovery worker panic recovered")
			}
		}()

		defer wg.Done()
		for ip := range jobs {
			pinger, err := probing.NewPinger(ip)
			if err != nil {
				continue // Skip invalid IP addresses
			}
			pinger.Count = 1                 // Single ping per device
			pinger.Timeout = 1 * time.Second // 1-second discovery timeout
			pinger.SetPrivileged(true)       // Use raw sockets for ICMP
			if err := pinger.Run(); err != nil {
				continue // Skip ping failures
			}
			stats := pinger.Statistics()
			if stats.PacketsRecv > 0 { // Device responded to ping
				results <- state.Device{
					IP:       ip,
					Hostname: ip, // Use IP as hostname for ping-discovered devices
					LastSeen: time.Now(),
				}
			}
		}
	}

	// Launch concurrent ping worker goroutines
	workerCount := icmpWorkers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer: enqueue all IPs from CIDR range
	streamIPsFromCIDR(cidr, jobs)
	close(jobs)

	// Wait for all workers to complete, then close results channel
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunPingDiscovery wait goroutine panic recovered")
			}
		}()

		wg.Wait()
		close(results)
	}()

	// Collect all ping-responsive devices
	var devices []state.Device
	for dev := range results {
		devices = append(devices, dev)
	}
	return devices
}

// RunFullDiscovery performs ICMP ping sweep first, then SNMP polling of online devices
func RunFullDiscovery(cfg *config.Config) []state.Device {
	var (
		jobs    = make(chan string, 256)       // Buffered channel for IP addresses to scan
		results = make(chan state.Device, 256) // Buffered channel for discovered devices
		wg      sync.WaitGroup
	)

	// Worker goroutine for SNMP queries (only on ping-responsive devices)
	worker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunFullDiscovery SNMP worker panic recovered")
			}
		}()

		defer wg.Done()
		for ip := range jobs {
			// Configure SNMP connection parameters
			params := &gosnmp.GoSNMP{
				Target:    ip,
				Port:      uint16(cfg.SNMP.Port),
				Community: cfg.SNMP.Community,
				Version:   gosnmp.Version2c,
				Timeout:   cfg.SNMP.Timeout,
				Retries:   cfg.SNMP.Retries,
			}
			if err := params.Connect(); err != nil {
				// SNMP failed, but device is online (from ICMP), so add basic device info
				results <- state.Device{
					IP:       ip,
					Hostname: ip, // Use IP as hostname for non-SNMP devices
					SysDescr: "ICMP-responsive device (SNMP unavailable)",
					LastSeen: time.Now(),
				}
				continue
			}
			// Query standard MIB-II system OIDs: sysName, sysDescr
			oids := []string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0"}
			resp, err := snmp.GetWithFallback(params, oids)
			params.Conn.Close()
			if err != nil || len(resp.Variables) < 2 {
				// SNMP query failed, but device is online
				results <- state.Device{
					IP:       ip,
					Hostname: ip,
					SysDescr: "ICMP-responsive device (SNMP query failed)",
					LastSeen: time.Now(),
				}
				continue
			}

			// Validate and sanitize SNMP response data
			hostname, err := snmp.ValidateString(resp.Variables[0].Value, "sysName")
			if err != nil {
				continue // Skip devices with invalid hostname data
			}
			sysDescr, err := snmp.ValidateString(resp.Variables[1].Value, "sysDescr")
			if err != nil {
				continue // Skip devices with invalid description data
			}

			dev := state.Device{
				IP:       ip,
				Hostname: hostname,
				SysDescr: sysDescr,
				LastSeen: time.Now(),
			}
			results <- dev
		}
	}

	// First, perform ICMP ping sweep to find online devices
	log.Info().Msg("Performing ICMP discovery to find online devices")
	onlineIPs := make([]string, 0)

	var icmpWg sync.WaitGroup
	icmpJobs := make(chan string, 256)
	icmpResults := make(chan string, 256)

	// ICMP worker goroutine
	icmpWorker := func() {
		// Panic recovery for worker goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunFullDiscovery ICMP worker panic recovered")
			}
		}()

		defer icmpWg.Done()
		for ip := range icmpJobs {
			pinger, err := probing.NewPinger(ip)
			if err != nil {
				continue
			}
			pinger.Count = 1
			pinger.Timeout = 1 * time.Second
			pinger.SetPrivileged(true)
			if err := pinger.Run(); err != nil {
				continue
			}
			stats := pinger.Statistics()
			if stats.PacketsRecv > 0 {
				icmpResults <- ip
			}
		}
	}

	// Launch ICMP workers
	icmpWorkerCount := cfg.IcmpWorkers
	for i := 0; i < icmpWorkerCount; i++ {
		icmpWg.Add(1)
		go icmpWorker()
	}

	// Producer: enqueue all IPs from all configured networks
	for _, network := range cfg.Networks {
		// Stream IPs directly to channel without intermediate array
		streamIPsFromCIDR(network, icmpJobs)
	}
	close(icmpJobs)

	// Wait for ICMP discovery to complete
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunFullDiscovery ICMP wait goroutine panic recovered")
			}
		}()

		icmpWg.Wait()
		close(icmpResults)
	}()

	// Collect online IPs
	for ip := range icmpResults {
		onlineIPs = append(onlineIPs, ip)
	}

	log.Info().
		Int("online_devices", len(onlineIPs)).
		Msg("ICMP discovery complete, starting SNMP polling")

	// Now perform SNMP polling only on online devices
	// Launch SNMP worker goroutines
	snmpWorkerCount := cfg.SnmpWorkers // Configurable SNMP workers
	for i := 0; i < snmpWorkerCount; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer: enqueue online IPs for SNMP polling
	for _, ip := range onlineIPs {
		jobs <- ip
	}
	close(jobs)

	// Wait for all SNMP workers to complete
	go func() {
		// Panic recovery for wait goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RunFullDiscovery SNMP wait goroutine panic recovered")
			}
		}()

		wg.Wait()
		close(results)
	}()

	// Collect all discovered devices
	var devices []state.Device
	for dev := range results {
		devices = append(devices, dev)
	}
	return devices
}

// streamIPsFromCIDR streams IP addresses from CIDR notation directly to a channel
// This avoids allocating memory for all IPs at once, significantly reducing memory usage
// Network and broadcast addresses are excluded for networks /30 and larger
func streamIPsFromCIDR(cidr string, ipChan chan<- string) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Error().
			Str("cidr", cidr).
			Err(err).
			Msg("Invalid CIDR")
		return
	}

	// Calculate network size for safety checks
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones

	// For networks larger than /16 (65K hosts), log warning
	// but still proceed (worker pool will rate-limit actual operations)
	if hostBits > 16 {
		log.Warn().
			Str("cidr", cidr).
			Int("host_bits", hostBits).
			Msg("Large network detected - scan may take significant time")
	}

	// Start from network address
	ip = ip.Mask(ipnet.Mask)

	// For /31 and /32 networks, there are no network/broadcast addresses to skip (RFC 3021)
	// For all other networks, skip the network address (first IP) and broadcast address (last IP)
	skipNetworkAndBroadcast := ones < 31

	if skipNetworkAndBroadcast {
		// Skip network address (first IP)
		incIP(ip)
	}

	// Stream usable host IPs directly to channel
	count := 0
	maxIPs := 1 << uint(hostBits) // Calculate actual network size
	if maxIPs > 65536 {
		maxIPs = 65536 // Safety limit
	}

	for ipnet.Contains(ip) && count < maxIPs {
		// For networks with network/broadcast addresses, stop before broadcast address
		if skipNetworkAndBroadcast {
			// Calculate broadcast address by checking if next IP would be outside network
			nextIP := make(net.IP, len(ip))
			copy(nextIP, ip)
			incIP(nextIP)
			if !ipnet.Contains(nextIP) {
				// Current IP is the broadcast address, stop here
				break
			}
		}

		ipChan <- ip.String()
		count++
		incIP(ip)
	}
}

// ipsFromCIDR expands CIDR notation into individual IP addresses
// NOTE: This function allocates memory for all IPs. For large networks, use streamIPsFromCIDR instead.
// This is kept for backward compatibility with RunScanIPsOnly
// Network and broadcast addresses are excluded for networks /30 and larger
func ipsFromCIDR(cidr string) []string {
	var ips []string
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ips
	}

	// Calculate network size to prevent memory exhaustion
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones

	// For networks larger than /16 (65K hosts), limit the expansion
	// to prevent memory exhaustion attacks
	maxIPs := 65536 // Maximum 64K IPs in memory at once
	if hostBits > 16 {
		// Network too large - would create millions of IPs
		// This should have been caught by config validation
		return ips
	}

	// Start from network address
	ip = ip.Mask(ipnet.Mask)

	// For /31 and /32 networks, there are no network/broadcast addresses to skip (RFC 3021)
	// For all other networks, skip the network address (first IP) and broadcast address (last IP)
	skipNetworkAndBroadcast := ones < 31

	if skipNetworkAndBroadcast {
		// Skip network address (first IP)
		incIP(ip)
	}

	// Iterate through usable host IPs in the subnet
	count := 0
	for ipnet.Contains(ip) {
		// For networks with network/broadcast addresses, stop before broadcast address
		if skipNetworkAndBroadcast {
			// Calculate broadcast address by checking if next IP would be outside network
			nextIP := make(net.IP, len(ip))
			copy(nextIP, ip)
			incIP(nextIP)
			if !ipnet.Contains(nextIP) {
				// Current IP is the broadcast address, stop here
				break
			}
		}

		ips = append(ips, ip.String())
		count++
		if count >= maxIPs {
			// Safety limit reached
			break
		}
		incIP(ip)
	}
	return ips
}

// incIP increments an IP address by 1 (handles carry-over for IPv4)
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// streamRandomizedNetwork generates IPs from a CIDR using a Linear Congruential Generator (LCG)
// to iterate through the address space in a pseudo-random order without buffering.
func streamRandomizedNetwork(ctx context.Context, cidr string, out chan<- string) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Error().
			Str("cidr", cidr).
			Err(err).
			Msg("Invalid CIDR")
		return
	}

	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits > 32 {
		return // Should not happen for IPv4
	}

	// Determine the range size
	size := uint64(1) << hostBits
	mask := size - 1

	// Generate LCG parameters
	// a must satisfy: a % 4 == 1
	// c must be odd
	a := (rand.Uint64() * 4) + 1
	c := rand.Uint64() | 1

	current := rand.Uint64() & mask

	// Prepare network address as uint32
	networkIP := ipToUint32(ipnet.IP)

	// For standard subnets, we skip first (network) and last (broadcast)
	// Exception: /31 (size 2) and /32 (size 1) don't have reserved addresses
	skipNetwork := ones < 31
	skipBroadcast := ones < 31

	for i := uint64(0); i < size; i++ {
		// Check context periodically (every 1000 iterations or so to avoid overhead)
		if i%1000 == 0 {
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		// Next LCG value
		current = (a*current + c) & mask

		// Skip network address (offset 0)
		if skipNetwork && current == 0 {
			continue
		}
		// Skip broadcast address (offset size-1)
		if skipBroadcast && current == mask {
			continue
		}

		// Calculate IP
		ipInt := networkIP + uint32(current)
		ip := uint32ToIP(ipInt)
		out <- ip.String()
	}
}

func ipToUint32(ip net.IP) uint32 {
	if len(ip) == 16 {
		return binary.BigEndian.Uint32(ip[12:16])
	}
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}
