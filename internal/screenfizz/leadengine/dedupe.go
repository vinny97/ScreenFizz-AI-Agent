package leadengine

import (
	"net/url"
	"strings"
)

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func UniqueEmails(emails []string) []string {
	seen := make(map[string]struct{}, len(emails))
	unique := make([]string, 0, len(emails))
	for _, email := range emails {
		normalized := NormalizeEmail(email)
		if normalized == "" {
			continue
		}
		if _, found := seen[normalized]; found {
			continue
		}
		seen[normalized] = struct{}{}
		unique = append(unique, normalized)
	}
	return unique
}

type dedupeSet struct {
	websites map[string]struct{}
}

func newDedupeSet() dedupeSet {
	return dedupeSet{
		websites: make(map[string]struct{}),
	}
}

func (s dedupeSet) add(business Business) {
	if website := normalizeWebsite(business.Website); website != "" {
		s.websites[website] = struct{}{}
	}
}

func duplicateBusiness(s dedupeSet, business Business) bool {
	if website := normalizeWebsite(business.Website); website != "" {
		if _, found := s.websites[website]; found {
			return true
		}
	}
	return false
}

func normalizeWebsite(website string) string {
	website = strings.ToLower(strings.TrimSpace(website))
	website = strings.TrimSuffix(website, "/")
	if website == "" {
		return ""
	}
	parsed, err := url.Parse(website)
	if err == nil && parsed.Host != "" {
		host := strings.TrimPrefix(parsed.Host, "www.")
		path := strings.TrimSuffix(parsed.EscapedPath(), "/")
		return host + path
	}
	website = strings.TrimPrefix(website, "https://")
	website = strings.TrimPrefix(website, "http://")
	return strings.TrimPrefix(website, "www.")
}
