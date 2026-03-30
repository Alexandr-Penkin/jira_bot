package web

import _ "embed"

//go:embed landing.html
var landingHTML []byte

//go:embed public/img/logo.jpeg
var logoJPEG []byte

// LandingHTML returns the embedded landing page content.
func LandingHTML() []byte {
	return landingHTML
}

// LogoJPEG returns the embedded logo image.
func LogoJPEG() []byte {
	return logoJPEG
}
