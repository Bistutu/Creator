package webwindow

// PopupAddress allows website popups and the empty document that websites
// commonly open before starting a sign-in flow.
func PopupAddress(address string) bool {
	return address == "" || address == "about:blank" || websiteAddress(address)
}
