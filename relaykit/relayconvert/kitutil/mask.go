package kitutil

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	maskURLPattern    = regexp.MustCompile(`(http|https)://[^\s/$.?#].[^\s]*`)
	maskDomainPattern = regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	maskIPPattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	// maskApiKeyPattern matches patterns like 'api_key:xxx' or "api_key:xxx" to mask the API key value
	maskApiKeyPattern = regexp.MustCompile(`(['"]?)api_key:([^\s'"]+)(['"]?)`)
	// Only known protocol fields in diagnostic context may bypass domain masking.
	maskThinkingFieldPattern        = regexp.MustCompile(`^(?:(?:body|request)\.)?thinking\.(?:(?:enabled|adaptive)\.)?display$`)
	maskFieldLabelPattern           = regexp.MustCompile("(?i)(?:^|[\\s{,])['\"`]?(?:field|parameter|param|property)['\"`]?\\s*(?:[:=]\\s*)?['\"`]?$")
	maskFieldDiagnosticStartPattern = regexp.MustCompile("(?i)(?:^|[\\r\\n])\\s*(?:(?:upstream )?(?:validation error|invalid request|error)\\s*:\\s*)?['\"`]?$")
	maskFieldValidationPattern      = regexp.MustCompile("(?i)^['\"`]?\\s*:\\s+(?:input should\\b|field required\\b|extra inputs\\b|invalid\\b|expected\\b|must\\b|should\\b)")
)

// maskHostTail returns the tail parts of a domain/host that should be preserved.
// It keeps 2 parts for likely country-code TLDs (e.g., co.uk, com.cn), otherwise keeps only the TLD.
func maskHostTail(parts []string) []string {
	if len(parts) < 2 {
		return parts
	}
	lastPart := parts[len(parts)-1]
	secondLastPart := parts[len(parts)-2]
	if len(lastPart) == 2 && len(secondLastPart) <= 3 {
		// Likely country code TLD like co.uk, com.cn
		return []string{secondLastPart, lastPart}
	}
	return []string{lastPart}
}

// maskHostForURL collapses subdomains and keeps only masked prefix + preserved tail.
// Example: api.openai.com -> ***.com, sub.domain.co.uk -> ***.co.uk
func maskHostForURL(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "***"
	}
	tail := maskHostTail(parts)
	return "***." + strings.Join(tail, ".")
}

// maskHostForPlainDomain masks a plain domain and reflects subdomain depth with multiple ***.
// Example: openai.com -> ***.com, api.openai.com -> ***.***.com, sub.domain.co.uk -> ***.***.co.uk
func maskHostForPlainDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}
	tail := maskHostTail(parts)
	numStars := max(len(parts)-len(tail), 1)
	stars := strings.TrimSuffix(strings.Repeat("***.", numStars), ".")
	return stars + "." + strings.Join(tail, ".")
}

// isDiagnosticFieldPath distinguishes known field names from identical hostnames.
// Unknown dotted strings remain masked even in validation errors.
func isDiagnosticFieldPath(message string, start, end int) bool {
	if !maskThinkingFieldPattern.MatchString(message[start:end]) {
		return false
	}
	// Do not exempt a field-shaped substring of a URL, address, or larger token.
	if start > 0 && strings.ContainsRune("/@._-?=&%", rune(message[start-1])) {
		return false
	}
	if end < len(message) && strings.ContainsRune("/@._-?#=%", rune(message[end])) {
		return false
	}
	if end+1 < len(message) && message[end] == ':' && message[end+1] >= '0' && message[end+1] <= '9' {
		return false
	}
	prefix := message[:start]
	if maskFieldLabelPattern.MatchString(prefix) {
		return true
	}
	// An enum hint alone must not exempt a hostname in surrounding prose.
	return maskFieldDiagnosticStartPattern.MatchString(prefix) && maskFieldValidationPattern.MatchString(message[end:])
}

// MaskSensitiveInfo masks sensitive information like URLs, IPs, and domain names in a string
// Example:
// http://example.com -> http://***.com
// https://api.test.org/v1/users/123?key=secret -> https://***.org/***/***/?key=***
// https://sub.domain.co.uk/path/to/resource -> https://***.co.uk/***/***
// 192.168.1.1 -> ***.***.***.***
// openai.com -> ***.com
// www.openai.com -> ***.***.com
// api.openai.com -> ***.***.com
func MaskSensitiveInfo(str string) string {
	// Mask URLs
	str = maskURLPattern.ReplaceAllStringFunc(str, func(urlStr string) string {
		u, err := url.Parse(urlStr)
		if err != nil {
			return urlStr
		}

		host := u.Host
		if host == "" {
			return urlStr
		}

		// Mask host with unified logic
		maskedHost := maskHostForURL(host)

		result := u.Scheme + "://" + maskedHost

		// Mask path
		if u.Path != "" && u.Path != "/" {
			pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
			maskedPathParts := make([]string, len(pathParts))
			for i := range pathParts {
				if pathParts[i] != "" {
					maskedPathParts[i] = "***"
				}
			}
			if len(maskedPathParts) > 0 {
				result += "/" + strings.Join(maskedPathParts, "/")
			}
		} else if u.Path == "/" {
			result += "/"
		}

		// Mask query parameters
		if u.RawQuery != "" {
			values, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				// If can't parse query, just mask the whole query string
				result += "?***"
			} else {
				maskedParams := make([]string, 0, len(values))
				for key := range values {
					maskedParams = append(maskedParams, key+"=***")
				}
				if len(maskedParams) > 0 {
					result += "?" + strings.Join(maskedParams, "&")
				}
			}
		}

		return result
	})

	// Mask plain domains, retaining only recognized diagnostic field paths.
	if domains := maskDomainPattern.FindAllStringIndex(str, -1); len(domains) > 0 {
		var masked strings.Builder
		masked.Grow(len(str))
		last := 0
		for _, domain := range domains {
			start, end := domain[0], domain[1]
			masked.WriteString(str[last:start])
			if isDiagnosticFieldPath(str, start, end) {
				masked.WriteString(str[start:end])
			} else {
				masked.WriteString(maskHostForPlainDomain(str[start:end]))
			}
			last = end
		}
		masked.WriteString(str[last:])
		str = masked.String()
	}

	// Mask IP addresses
	str = maskIPPattern.ReplaceAllString(str, "***.***.***.***")

	// Mask API keys (e.g., "api_key:AIzaSyAAAaUooTUni8AdaOkSRMda30n_Q4vrV70" -> "api_key:***")
	str = maskApiKeyPattern.ReplaceAllString(str, "${1}api_key:***${3}")

	return str
}
