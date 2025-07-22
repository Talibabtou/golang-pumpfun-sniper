package utils

import (
	"net/url"
	"regexp"
	"strings"
)

func SanitizeURL(rawURL string) string {
	if rawURL == "" {
		return "unknown"
	}
	
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	
	parsedURL.RawQuery = ""
	
	host := parsedURL.Host
	if strings.Contains(host, ".") {
		parts := strings.Split(host, ".")
		if len(parts) > 2 {
			parts[0] = parts[0][:min(3, len(parts[0]))] + "***"
		}
		parsedURL.Host = strings.Join(parts, ".")
	}
	
	return parsedURL.String()
}

func SanitizePrivateKey(key string) string {
	if key == "" {
		return "not-set"
	}
	return "***PRIVATE-KEY-HIDDEN***"
}

func SanitizeWalletAddress(address string) string {
	if len(address) < 8 {
		return "***"
	}
	return address[:4] + "..." + address[len(address)-4:]
}

func SanitizeError(err error, rpcURL string) string {
	if err == nil {
		return ""
	}
	
	errMsg := err.Error()
	
	if rpcURL != "" {
		sanitizedURL := SanitizeURL(rpcURL)
		errMsg = strings.ReplaceAll(errMsg, rpcURL, sanitizedURL)
	}
	
	patterns := []string{
		`api-key=[a-zA-Z0-9-]+`,
		`token=[a-zA-Z0-9-]+`,
		`key=[a-zA-Z0-9-]+`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		errMsg = re.ReplaceAllString(errMsg, "***API-KEY-HIDDEN***")
	}
	
	return errMsg
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SanitizeAddress is an alias for SanitizeWalletAddress
func SanitizeAddress(address string) string {
	return SanitizeWalletAddress(address)
}
