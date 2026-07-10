package leadengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ukPostcodePattern = regexp.MustCompile(`(?i)\b[A-Z]{1,2}\d[A-Z\d]?\s*\d[A-Z]{2}\b`)

// Business is the normalized ScreenFizz business shape before insertion into
// the ScreenFizz-owned businesses table.
type Business struct {
	ID                string     `json:"id,omitempty"`
	BusinessName      string     `json:"business_name,omitempty"`
	Category          string     `json:"category,omitempty"`
	Website           string     `json:"website,omitempty"`
	Email             string     `json:"email,omitempty"`
	Phone             string     `json:"phone,omitempty"`
	Address           string     `json:"address,omitempty"`
	Town              string     `json:"town,omitempty"`
	Postcode          string     `json:"postcode,omitempty"`
	GoogleMapsURL     string     `json:"google_maps_url,omitempty"`
	Rating            *float64   `json:"rating,omitempty"`
	ReviewCount       *int       `json:"review_count,omitempty"`
	Latitude          *float64   `json:"latitude,omitempty"`
	Longitude         *float64   `json:"longitude,omitempty"`
	PermanentlyClosed bool       `json:"-"`
	Contacted         bool       `json:"contacted,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	Source            string     `json:"source,omitempty"`
}

type ImportResult struct {
	Imported          int
	NoWebsiteSkipped  int
	NoEmailSkipped    int
	ClosedSkipped     int
	DuplicatesSkipped int
	ProspectsAdded    int
	ProspectsSkipped  int
}

// Importer will own ScreenFizz business insertion logic. It is deliberately
// separate from the Influocial importer.
type Importer struct {
	Config     Config
	httpClient *http.Client
}

func NewImporter(cfg Config) *Importer {
	return &Importer{Config: cfg, httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (i *Importer) ImportDataset(ctx context.Context, dataset json.RawMessage) (ImportResult, error) {
	businesses, err := DecodeBusinesses(dataset)
	if err != nil {
		return ImportResult{}, err
	}
	existing, err := i.existingDedupeKeys(ctx)
	if err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{}
	newRows := make([]map[string]any, 0, len(businesses))
	seen := newDedupeSet()
	for _, business := range businesses {
		switch {
		case strings.TrimSpace(business.Website) == "":
			result.NoWebsiteSkipped++
			logSkippedBusiness(business, "no_website")
			continue
		case strings.TrimSpace(business.Email) == "":
			result.NoEmailSkipped++
			logSkippedBusiness(business, "no_email")
			continue
		case business.PermanentlyClosed:
			result.ClosedSkipped++
			logSkippedBusiness(business, "closed")
			continue
		}
		if duplicateBusiness(existing, business) || duplicateBusiness(seen, business) {
			result.DuplicatesSkipped++
			logSkippedBusiness(business, "duplicate")
			continue
		}
		seen.add(business)
		newRows = append(newRows, businessInsertRow(business))
	}
	if len(newRows) > 0 {
		if err := i.insertBusinesses(ctx, newRows); err != nil {
			return ImportResult{}, err
		}
		result.Imported = len(newRows)
	}
	prospects, err := SyncProspects(ctx, i.Config)
	if err != nil {
		return ImportResult{}, err
	}
	result.ProspectsAdded = prospects.Added
	result.ProspectsSkipped = prospects.Skipped
	return result, nil
}

func DecodeBusinesses(dataset json.RawMessage) ([]Business, error) {
	var items []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(dataset))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("decode Apify dataset: %w", err)
	}
	businesses := make([]Business, 0, len(items))
	for _, item := range items {
		businesses = append(businesses, businessFromApifyItem(item))
	}
	return businesses, nil
}

func businessFromApifyItem(item map[string]any) Business {
	address := firstString(item, "address", "street", "streetAddress")
	return Business{
		BusinessName:      firstString(item, "business_name", "businessName", "title", "name", "placeName"),
		Category:          firstString(item, "category", "categoryName", "searchString", "type"),
		Website:           normalizeWebsite(firstString(item, "website", "websiteUrl", "domain")),
		Email:             NormalizeEmail(firstString(item, "email", "emails", "contactEmail")),
		Phone:             firstString(item, "phone", "phoneNumber", "telephone"),
		Address:           address,
		Town:              firstString(item, "town", "city", "municipality"),
		Postcode:          firstNonEmpty(firstString(item, "postcode", "postalCode", "zip"), postcodeFromAddress(address)),
		GoogleMapsURL:     firstString(item, "google_maps_url", "googleMapsUrl", "url", "placeUrl"),
		Rating:            firstFloat(item, "rating", "totalScore", "stars"),
		ReviewCount:       firstInt(item, "review_count", "reviewCount", "reviewsCount", "numberOfReviews"),
		Latitude:          firstFloat(item, "latitude", "lat"),
		Longitude:         firstFloat(item, "longitude", "lng", "lon"),
		PermanentlyClosed: firstBool(item, "permanentlyClosed", "isPermanentlyClosed"),
		Source:            "apify:compass/crawler-google-places",
	}
}

func logSkippedBusiness(business Business, reason string) {
	slog.Info("screenfizz.leadengine.business_skipped",
		"business_name", business.BusinessName,
		"website", business.Website,
		"reason", reason)
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		value, found := item[key]
		if !found || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case []any:
			for _, entry := range v {
				switch e := entry.(type) {
				case string:
					text := e
					if strings.TrimSpace(text) != "" {
						return strings.TrimSpace(text)
					}
				case map[string]any:
					text := firstString(e, "email", "value", "url", "href")
					if text != "" {
						return text
					}
				}
			}
		case map[string]any:
			for _, nestedKey := range []string{"email", "value", "url", "href"} {
				text, ok := v[nestedKey].(string)
				if ok && text != "" {
					return strings.TrimSpace(text)
				}
			}
		case json.Number:
			if v.String() != "" {
				return v.String()
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func postcodeFromAddress(address string) string {
	postcode := ukPostcodePattern.FindString(address)
	return strings.ToUpper(strings.Join(strings.Fields(postcode), " "))
}

func firstFloat(item map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		value, found := item[key]
		if !found || value == nil {
			continue
		}
		switch v := value.(type) {
		case json.Number:
			n, err := v.Float64()
			if err == nil {
				return &n
			}
		case float64:
			return &v
		case string:
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil {
				return &n
			}
		}
	}
	return nil
}

func firstInt(item map[string]any, keys ...string) *int {
	for _, key := range keys {
		value, found := item[key]
		if !found || value == nil {
			continue
		}
		switch v := value.(type) {
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				i := int(n)
				return &i
			}
		case float64:
			i := int(v)
			return &i
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err == nil {
				return &n
			}
		}
	}
	return nil
}

func firstBool(item map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, found := item[key]
		if !found || value == nil {
			continue
		}
		switch v := value.(type) {
		case bool:
			return v
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(v))
			if err == nil {
				return parsed
			}
		}
	}
	return false
}

func businessInsertRow(business Business) map[string]any {
	return map[string]any{
		"business_name":   business.BusinessName,
		"category":        business.Category,
		"website":         business.Website,
		"email":           business.Email,
		"phone":           business.Phone,
		"address":         business.Address,
		"town":            business.Town,
		"postcode":        business.Postcode,
		"latitude":        business.Latitude,
		"longitude":       business.Longitude,
		"google_maps_url": business.GoogleMapsURL,
		"rating":          business.Rating,
		"review_count":    business.ReviewCount,
		"source":          business.Source,
		"created_at":      time.Now().UTC().Format(time.RFC3339),
	}
}

func (i *Importer) insertBusinesses(ctx context.Context, rows []map[string]any) error {
	body, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("encode ScreenFizz businesses: %w", err)
	}
	requestURL := strings.TrimRight(i.Config.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(i.Config.BusinessesTable)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ScreenFizz businesses insert request: %w", err)
	}
	i.setSupabaseHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("insert ScreenFizz businesses: %w", err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read ScreenFizz businesses insert response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("insert ScreenFizz businesses: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (i *Importer) existingDedupeKeys(ctx context.Context) (dedupeSet, error) {
	keys := newDedupeSet()
	for start := 0; ; {
		requestURL := strings.TrimRight(i.Config.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(i.Config.BusinessesTable) + "?select=website"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return dedupeSet{}, fmt.Errorf("create ScreenFizz businesses list request: %w", err)
		}
		i.setSupabaseHeaders(req)
		req.Header.Set("Range", fmt.Sprintf("%d-%d", start, start+999))
		req.Header.Set("Prefer", "count=exact")
		resp, err := i.httpClient.Do(req)
		if err != nil {
			return dedupeSet{}, fmt.Errorf("list ScreenFizz businesses: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return dedupeSet{}, fmt.Errorf("read ScreenFizz businesses list response: %w", err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return dedupeSet{}, fmt.Errorf("list ScreenFizz businesses: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		var page []Business
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&page); err != nil {
			return dedupeSet{}, fmt.Errorf("decode ScreenFizz businesses list response: %w", err)
		}
		for _, business := range page {
			keys.add(business)
		}
		total, ok := contentRangeTotal(resp.Header.Get("Content-Range"))
		if len(page) == 0 || (ok && start+len(page) >= total) || (!ok && len(page) < 1000) {
			return keys, nil
		}
		start += len(page)
	}
}

func contentRangeTotal(value string) (int, bool) {
	_, rawTotal, ok := strings.Cut(value, "/")
	if !ok || rawTotal == "*" {
		return 0, false
	}
	total, err := strconv.Atoi(rawTotal)
	return total, err == nil
}

func (i *Importer) setSupabaseHeaders(req *http.Request) {
	setSupabaseHeaders(req, i.Config)
}
