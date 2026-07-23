package web

import (
	"embed"
)

//go:embed index.html
var IndexHTML []byte

//go:embed ads-preview.html
var AdsPreviewHTML []byte

//go:embed favicon.ico
var FaviconICO []byte

//go:embed robots.txt
var RobotsTXT []byte

//go:embed site.webmanifest
var SiteManifest []byte

//go:embed css/style.min.css
var TailwindCSS []byte

//go:embed js/htmx.min.js
var HtmxJS []byte

//go:embed fonts/*.woff2
var FontsFS embed.FS

//go:embed content/*.html
var ContentFS embed.FS

//go:embed images/*
var ImagesFS embed.FS
