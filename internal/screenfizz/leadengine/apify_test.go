package leadengine

import (
	"encoding/json"
	"testing"
)

func TestDefaultApifyInputMatchesScreenFizzSearch(t *testing.T) {
	input := DefaultApifyInput("Oxfordshire")
	if input.LocationQuery != "Oxfordshire, England" {
		t.Fatalf("LocationQuery = %q, want Oxfordshire, England", input.LocationQuery)
	}
	if input.MaxCrawledPlacesPerSearch != 100 {
		t.Fatalf("MaxCrawledPlacesPerSearch = %d, want 100", input.MaxCrawledPlacesPerSearch)
	}
	if !input.SkipClosedPlaces {
		t.Fatal("SkipClosedPlaces should be true")
	}
	if !input.ScrapeContacts {
		t.Fatal("ScrapeContacts should be true")
	}
	if input.ScrapePlaceDetailPage {
		t.Fatal("ScrapePlaceDetailPage should be false")
	}
	if len(input.SearchStringsArray) != 12 {
		t.Fatalf("SearchStringsArray length = %d, want 12", len(input.SearchStringsArray))
	}
}

func TestDefaultApifyCampaignUsesTokenlessActorURL(t *testing.T) {
	campaign, err := DefaultApifyCampaign(Config{ApifyAPIURL: defaultApifyAPIURL}, "Bedfordshire")
	if err != nil {
		t.Fatalf("DefaultApifyCampaign() error = %v", err)
	}
	if campaign.ApifyAPIURL != defaultApifyAPIURL {
		t.Fatalf("ApifyAPIURL = %q, want %q", campaign.ApifyAPIURL, defaultApifyAPIURL)
	}
	var input ApifyInput
	if err := json.Unmarshal(campaign.ApifyInput, &input); err != nil {
		t.Fatalf("ApifyInput is not valid JSON: %v", err)
	}
	if input.Language != "en" {
		t.Fatalf("Language = %q, want en", input.Language)
	}
}

func TestTestApifyCampaignMatchesRequestedInput(t *testing.T) {
	campaign, err := TestApifyCampaign(Config{ApifyAPIURL: defaultApifyAPIURL}, "Hertfordshire")
	if err != nil {
		t.Fatalf("TestApifyCampaign() error = %v", err)
	}
	var input ApifyInput
	if err := json.Unmarshal(campaign.ApifyInput, &input); err != nil {
		t.Fatalf("ApifyInput is not valid JSON: %v", err)
	}
	if len(input.SearchStringsArray) != 1 || input.SearchStringsArray[0] != "restaurant" {
		t.Fatalf("SearchStringsArray = %#v, want [restaurant]", input.SearchStringsArray)
	}
	if input.LocationQuery != "Hertfordshire, England" || input.MaxCrawledPlacesPerSearch != 5 || input.Language != "en" || !input.SkipClosedPlaces || !input.ScrapeContacts || input.ScrapePlaceDetailPage {
		t.Fatalf("unexpected test input: %#v", input)
	}
}
