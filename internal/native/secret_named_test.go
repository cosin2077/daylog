package native

import "testing"

func TestIntegrationCredentialNamesAndValues(t *testing.T) {
	for _, name := range []string{"", "../../AI", "notion\x00bad"} {
		if validateNamedSecret(name, "") == nil {
			t.Fatal("bad name accepted")
		}
	}
	if err := validateNamedSecret("notion.0123456789abcdef", "safe-placeholder"); err != nil {
		t.Fatal(err)
	}
	if validateNamedSecret("notion.example", "two\nlines") == nil {
		t.Fatal("multiline credential accepted")
	}
}
