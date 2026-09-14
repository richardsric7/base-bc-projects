// Package controllers serves this service's OpenAPI 3.0 specification and
// an interactive Swagger UI page (DEPLOYMENT.md §12) - the reference for
// integrating against every one of this API's routes, their auth scheme,
// and their request/response shapes, rather than reading route
// registrations out of each component's own controllers.go.
//
// The spec (assets/openapi.json) is hand-authored, not generated from Go
// struct tags or handler comments - this codebase has ~150 routes spread
// across 19 components, and per-handler annotation comments (the usual
// swaggo/swag approach) would be a large ongoing maintenance surface for
// marginal benefit over a spec kept in sync by hand alongside each
// component's own PLAN.md implementation notes. It's embedded into the
// binary (go:embed) so there is nothing extra to deploy or keep in sync
// at runtime - update assets/openapi.json and rebuild.
package controllers

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed assets/openapi.json
var openapiSpec []byte

// Init registers the docs component's routes on router: the raw spec at
// GET /swagger/openapi.json and a Swagger UI page at GET /swagger/ that
// loads it. Swagger UI's own JS/CSS assets load from a CDN
// (cdn.jsdelivr.net, already on this project's Content-Security-Policy
// allowlist convention for external scripts - see wallet-web's own CSP
// note in DEPLOYMENT.md for the parallel) - only the browser viewing the
// page needs that reachable, not this server, and openapi.json itself is
// fully self-hosted and usable offline with any other OpenAPI tool.
func Init(router *gin.Engine) {
	router.GET("/swagger/openapi.json", serveSpec)
	router.GET("/swagger/", serveSwaggerUI)
	router.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/swagger/")
	})
}

func serveSpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", openapiSpec)
}

func serveSwaggerUI(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>wallet-backend API docs</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
  <style>body { margin: 0; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: '/swagger/openapi.json',
        dom_id: '#swagger-ui',
        presets: [SwaggerUIBundle.presets.apis],
        layout: 'BaseLayout',
        deepLinking: true,
      });
    };
  </script>
</body>
</html>`
