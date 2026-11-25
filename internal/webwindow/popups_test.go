package webwindow

import "testing"

func TestPopupAddress(t *testing.T) {
	for _, address := range []string{"", "about:blank", "https://example.com/login?next=%2Fhome", "http://localhost:8080/"} {
		if !PopupAddress(address) {
			t.Errorf("popup address %q was rejected", address)
		}
	}
	for _, address := range []string{"file:///tmp/local.html", "javascript:void(0)", "https://user@example.com/", "https:///missing-host"} {
		if PopupAddress(address) {
			t.Errorf("popup address %q was allowed", address)
		}
	}
}
