package nvmehost

import (
	"github.com/google/uuid"
	"testing"
)

func TestRemoveDash(t *testing.T) {
	goodUUID := uuid.New().String()

	if !isValidUUID(removeDash(goodUUID)) {
		t.Errorf("sanity failed")
	}
	if !isValidUUID(removeDash(goodUUID + "\n\n")) {
		t.Errorf("fails with file with 2 new lines")
	}
	if isValidUUID("") {
		t.Errorf("empty uuid should not be valid")
	}
	if !isValidUUID(removeDash("\n" + goodUUID + "\n")) {
		t.Errorf("fails validate content of uuid wrapped with new lines")
	}
}
