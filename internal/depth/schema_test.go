package depth

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestSchemaTokens checks that schema/depth.schema.json lists exactly the
// reason tokens and checks mtf-facts depth writes, in canonical order, so
// neither can change without the other.
func TestSchemaTokens(t *testing.T) {
	data, err := os.ReadFile("../../schema/depth.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties struct {
			Reasons struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"reasons"`
			Checks struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"checks_out_of_scope"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Properties.Reasons.Items.Enum; !slices.Equal(got, Reasons) {
		t.Errorf("schema reasons %v, want %v", got, Reasons)
	}
	if got, want := s.Properties.Checks.Items.Enum, []string{CheckA, CheckB, CheckD, CheckGLadder}; !slices.Equal(got, want) {
		t.Errorf("schema checks %v, want %v", got, want)
	}
}
