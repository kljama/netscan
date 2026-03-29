package snmp

import (
	"fmt"
	"strings"

	"github.com/gosnmp/gosnmp"
	"github.com/rs/zerolog/log"
)

// SNMPClient interface for SNMP operations to allow mocking
type SNMPClient interface {
	Get(oids []string) (*gosnmp.SnmpPacket, error)
	GetNext(oids []string) (*gosnmp.SnmpPacket, error)
	GetTarget() string
}

// GoSNMPWrapper wraps gosnmp.GoSNMP to implement SNMPClient
type GoSNMPWrapper struct {
	*gosnmp.GoSNMP
}

// GetTarget returns the target IP address
func (w *GoSNMPWrapper) GetTarget() string {
	return w.Target
}

// GetWithFallback attempts to get SNMP OIDs using Get, falling back to GetNext if Get fails
func GetWithFallback(client SNMPClient, oids []string) (*gosnmp.SnmpPacket, error) {
	// Try Get first (most efficient for .0 instances)
	resp, err := client.Get(oids)
	if err == nil {
		// Check if we got valid responses (no NoSuchInstance errors)
		hasValidData := false
		for _, variable := range resp.Variables {
			if variable.Type != gosnmp.NoSuchInstance && variable.Type != gosnmp.NoSuchObject {
				hasValidData = true
				break
			}
		}
		if hasValidData {
			return resp, nil
		}
		// All variables returned NoSuchInstance/NoSuchObject, try GetNext
		log.Debug().
			Str("target", client.GetTarget()).
			Msg("Get returned NoSuchInstance, trying GetNext fallback")
	}

	// Fallback to GetNext for each OID (works when .0 instance doesn't exist)
	baseOIDs := make([]string, len(oids))
	for i, oid := range oids {
		// Remove the .0 suffix if present to get base OID
		if len(oid) > 2 && oid[len(oid)-2:] == ".0" {
			baseOIDs[i] = oid[:len(oid)-2]
		} else {
			baseOIDs[i] = oid
		}
	}

	variables := make([]gosnmp.SnmpPDU, 0, len(baseOIDs))
	// Optimize: Use single GetNext for all OIDs
	resp, err = client.GetNext(baseOIDs)
	if err == nil && len(resp.Variables) > 0 {
		for i, variable := range resp.Variables {
			// Ensure we don't go out of bounds if response has more variables than requests
			if i >= len(baseOIDs) {
				break
			}
			baseOID := baseOIDs[i]

			// Verify the returned OID is under the requested base OID
			returnedOID := variable.Name
			if len(returnedOID) >= len(baseOID) && returnedOID[:len(baseOID)] == baseOID {
				variables = append(variables, variable)
			}
		}
	}

	if len(variables) == 0 {
		return nil, fmt.Errorf("no valid SNMP data retrieved")
	}

	// Construct a response packet with the collected variables
	return &gosnmp.SnmpPacket{
		Variables: variables,
	}, nil
}

// ValidateString validates and sanitizes SNMP string values
func ValidateString(value interface{}, oidName string) (string, error) {
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		return "", fmt.Errorf("invalid type for %s: expected string or []byte, got %T", oidName, value)
	}

	// Security: reject strings containing null bytes
	for i := 0; i < len(str); i++ {
		if str[i] == 0 {
			return "", fmt.Errorf("%s contains null byte at position %d", oidName, i)
		}
	}

	// Limit string length to prevent memory exhaustion
	if len(str) > 1024 {
		str = str[:1024]
	}

	// Sanitize: replace newlines and tabs with spaces, remove other non-printable chars
	// and common extended characters
	sanitized := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' ' // Replace newlines/tabs with spaces
		}
		if r < 32 || r > 126 { // Non-printable ASCII
			return -1 // Remove character
		}
		return r
	}, str)

	// Trim whitespace
	result := strings.TrimSpace(sanitized)

	if len(result) == 0 {
		return "", fmt.Errorf("%s is empty after sanitization", oidName)
	}

	return result, nil
}
