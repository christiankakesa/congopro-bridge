package templates

import (
	"crypto/md5"
	"fmt"

	"congopro-bridge/internal/web"
)

// CSSVersion is the content hash of the embedded Tailwind stylesheet, used to
// cache-bust /css/style.min.css links — the asset itself is served with a
// one-year immutable Cache-Control, so every layout must link it with ?h=.
var CSSVersion = fmt.Sprintf("%.8x", md5.Sum(web.TailwindCSS))

// AppJSVersion cache-busts /js/app.js the same way.
var AppJSVersion = fmt.Sprintf("%.8x", md5.Sum(web.AppJS))
