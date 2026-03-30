package web

import _ "embed"

//go:embed landing.html
var landingHTML []byte

//go:embed privacy.html
var privacyHTML []byte

//go:embed public/img/logo.jpeg
var logoJPEG []byte

// LandingHTML returns the embedded landing page content.
func LandingHTML() []byte {
	return landingHTML
}

// PrivacyHTML returns the embedded privacy policy page content.
func PrivacyHTML() []byte {
	return privacyHTML
}

// LogoJPEG returns the embedded logo image.
func LogoJPEG() []byte {
	return logoJPEG
}
