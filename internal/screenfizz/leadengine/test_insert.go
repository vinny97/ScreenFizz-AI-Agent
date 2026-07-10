package leadengine

import "context"

// InsertTestBusiness inserts the fixed ScreenFizz business used to verify the
// Supabase connection and table schema.
func InsertTestBusiness(ctx context.Context, cfg Config) error {
	return NewImporter(cfg).insertBusinesses(ctx, []map[string]any{businessInsertRow(Business{
		BusinessName: "Test Restaurant",
		Category:     "restaurant",
		Website:      "https://testrestaurant.co.uk",
		Email:        "hello@testrestaurant.co.uk",
		Phone:        "01234 567890",
		Address:      "1 High Street",
		Town:         "Milton Keynes",
		Postcode:     "MK9 1AA",
		Source:       "test",
	})})
}
