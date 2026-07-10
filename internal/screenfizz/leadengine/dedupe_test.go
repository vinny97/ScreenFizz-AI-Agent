package leadengine

import (
	"reflect"
	"testing"
)

func TestUniqueEmailsNormalizesAndDedupes(t *testing.T) {
	got := UniqueEmails([]string{" V@EXAMPLE.COM ", "v@example.com", "", "TEAM@example.com"})
	want := []string{"v@example.com", "team@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueEmails() = %#v, want %#v", got, want)
	}
}
