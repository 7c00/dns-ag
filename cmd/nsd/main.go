package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const fallbackDNS = "223.6.6.6:53"
const addr = ":53"

// Cache entry for DNS query results
type cacheEntry struct {
	msg      *dns.Msg
	expireAt time.Time
}

// Global variables to store routed IPs and DNS query cache
var (
	routedIPs   = make(map[string]bool)
	routedIPsMu sync.RWMutex

	// DNS query cache
	queryCache   = make(map[string]cacheEntry)
	cacheMu      sync.RWMutex
	cacheTimeout = 30 * time.Second
)

// parseIPRoute parses the output of "ip route" command and returns IPs routed via tun0
func parseIPRoute(output string) map[string]bool {
	ips := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Look for lines containing "dev tun0"
		if strings.Contains(line, "dev tun0") {
			// Extract IP addresses from the line
			parts := strings.Fields(line)
			for _, part := range parts {
				// Check if this part is an IP address
				if isValidIP(part) {
					ips[part] = true
				}
			}
		}
	}
	return ips
}

// isValidIP checks if a string is a valid IP address
func isValidIP(ipStr string) bool {
	// Check if it's IPv4
	if ip := net.ParseIP(ipStr); ip != nil {
		return true
	}
	return false
}

// addIPRoute adds an IP route via tun0
func addIPRoute(ip string) error {
	cmd := exec.Command("ip", "route", "add", ip, "via", "10.0.0.33", "dev", "tun0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to add route for IP %s: %v - Output: %s", ip, err, string(output))
		return err
	}
	log.Printf("Successfully added route for IP %s via tun0", ip)
	return nil
}

// ensureIPRoute ensures that an IP is routed via tun0
func ensureIPRoute(ip string) error {
	// Validate IP before processing
	if !isValidIP(ip) {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	routedIPsMu.Lock()
	defer routedIPsMu.Unlock()

	// Check if IP is already routed
	if routedIPs[ip] {
		return nil
	}

	// Add the route
	if err := addIPRoute(ip); err != nil {
		return err
	}

	// Mark IP as routed
	routedIPs[ip] = true
	return nil
}

// initializeRoutedIPs initializes the global set of IPs routed via tun0
func initializeRoutedIPs() {
	log.Println("Initializing routed IPs from ip route...")

	cmd := exec.Command("ip", "route")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to run 'ip route' command: %v", err)
		return
	}

	routedIPsMu.Lock()
	defer routedIPsMu.Unlock()

	routedIPs = parseIPRoute(string(output))
	log.Printf("Found %d IPs already routed via tun0", len(routedIPs))

	for ip := range routedIPs {
		log.Printf("Already routed: %s", ip)
	}
}

func extractIPs(ans []dns.RR) []string {
	var ips []string
	for _, r := range ans {
		switch record := r.(type) {
		case *dns.A:
			if record.A != nil {
				ips = append(ips, record.A.String())
			}
		case *dns.AAAA:
			if record.AAAA != nil {
				// Handle IPv4-mapped IPv6 addresses
				if record.AAAA.To4() != nil {
					ips = append(ips, "::ffff:"+record.AAAA.String())
				} else {
					ips = append(ips, record.AAAA.String())
				}
			}
		}
	}
	return ips
}

// generateCacheKey creates a unique key for caching DNS queries
func generateCacheKey(qName string, qType uint16) string {
	return fmt.Sprintf("%s:%d", qName, qType)
}

// getFromCache retrieves a DNS response from cache if it exists and is not expired
func getFromCache(qName string, qType uint16) *dns.Msg {
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	key := generateCacheKey(qName, qType)
	entry, exists := queryCache[key]
	if !exists {
		return nil
	}

	// Check if entry is expired
	if time.Now().After(entry.expireAt) {
		return nil
	}

	log.Printf("Cache hit for [%s,%d]", qName, qType)
	return entry.msg
}

// putInCache stores a DNS response in the cache with expiration
func putInCache(qName string, qType uint16, msg *dns.Msg) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	key := generateCacheKey(qName, qType)
	queryCache[key] = cacheEntry{
		msg:      msg,
		expireAt: time.Now().Add(cacheTimeout),
	}

	log.Printf("Cached response for [%s,%d], expires in %v", qName, qType, cacheTimeout)
}

// cleanExpiredCache removes expired entries from the cache
func cleanExpiredCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	now := time.Now()
	for key, entry := range queryCache {
		if now.After(entry.expireAt) {
			delete(queryCache, key)
		}
	}
}

func main() {
	// Initialize routed IPs from current ip route configuration
	initializeRoutedIPs()

	// Start background cache cleanup goroutine
	go func() {
		ticker := time.NewTicker(60 * time.Second) // Clean every minute
		defer ticker.Stop()
		for range ticker.C {
			cleanExpiredCache()
		}
	}()

	server := &dns.Server{
		Addr: addr,
		Net:  "udp",
	}
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// Validate that we have at least one question
		if len(r.Question) == 0 {
			log.Printf("Invalid DNS query: no question")
			return
		}

		qName := r.Question[0].Name
		qType := r.Question[0].Qtype

		// Validate question name
		if qName == "" {
			log.Printf("Invalid DNS query: empty question name")
			return
		}

		if qType == 28 {
			log.Printf("Ignored AAAA Query to %s", qName)
			msg := new(dns.Msg)
			msg.SetReply(r)
			msg.Rcode = dns.RcodeNameError
			if err := w.WriteMsg(msg); err != nil {
				log.Printf("Error writing response: %v", err)
			}
			return
		}

		log.Printf("Querying for [%s,%d]", qName, qType)

		// Check cache first
		if cachedResp := getFromCache(qName, qType); cachedResp != nil {
			log.Printf("Using cached response for [%s,%d]", qName, qType)
			msg := new(dns.Msg)
			msg.SetReply(r)
			msg.Answer = cachedResp.Answer
			if err := w.WriteMsg(msg); err != nil {
				log.Printf("Error writing cached response: %v", err)
			}
			return
		}

		// Cache miss - query fallback server
		client := &dns.Client{}
		fallbackResp, _, err := client.Exchange(r, fallbackDNS)
		if err != nil {
			log.Printf("Querying for [%s,%d]: Error querying fallback DNS server: %v", qName, qType, err)
			msg := new(dns.Msg)
			msg.SetReply(r)
			msg.Rcode = dns.RcodeServerFailure
			if err := w.WriteMsg(msg); err != nil {
				log.Printf("Error writing response: %v", err)
			}
			return
		}

		if len(fallbackResp.Answer) == 0 {
			log.Printf("Querying for [%s,%d]: No answer from fallback DNS server", qName, qType)
			msg := new(dns.Msg)
			msg.SetReply(r)
			msg.Rcode = dns.RcodeNameError
			if err := w.WriteMsg(msg); err != nil {
				log.Printf("Error writing response: %v", err)
			}
			return
		}

		msg := new(dns.Msg)
		msg.SetReply(r)
		ips := extractIPs(fallbackResp.Answer)
		log.Printf("Querying for [%s,%d]: Answer: %v", qName, qType, ips)

		// Check if this is a googleapis domain and add routes for IPs
		lowerQName := strings.ToLower(qName)
		if strings.HasSuffix(lowerQName, "googleapis.com.") ||
			strings.HasSuffix(lowerQName, "googleusercontent.com.") ||
			strings.HasSuffix(lowerQName, "antigravity-unleash.goog.") {
			log.Printf("Detected Google domain: %s, adding routes for IPs: %v", qName, ips)
			for _, ip := range ips {
				if err := ensureIPRoute(ip); err != nil {
					log.Printf("Failed to ensure route for IP %s: %v", ip, err)
				} else {
					log.Printf("Successfully added route for IP %s via tun0", ip)
				}
			}
		}

		msg.Answer = append(msg.Answer, fallbackResp.Answer...)

		// Cache the successful response
		putInCache(qName, qType, msg)

		if err := w.WriteMsg(msg); err != nil {
			log.Printf("Error writing response: %v", err)
		}
	})

	log.Println("Starting DNS server on port ", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal("Error starting DNS server:", err)
	}
}
