package web

import (
	_ "embed"
)

//go:embed index.html
var IndexHTML []byte

//go:embed favicon.ico
var FaviconICO []byte

//go:embed css/style.min.css
var TailwindCSS []byte

//go:embed content/help.html
var HelpHTML []byte

//go:embed content/privacy.html
var PrivacyHTML []byte

//go:embed content/terms.html
var TermsHTML []byte

//go:embed robots.txt
var RobotsTXT []byte
