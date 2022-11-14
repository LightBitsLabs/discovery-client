// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
