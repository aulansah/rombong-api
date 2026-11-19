// Package docs embeds the hand-written OpenAPI specification.
//
// The spec is written first and kept next to the code instead of being
// generated from annotations, which keeps handlers free of comment noise and
// the build free of a code generation step.
package docs

import _ "embed"

// OpenAPI is the raw OpenAPI 3 document served at /docs/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte

// SwaggerUI is a minimal page that renders the spec with Swagger UI.
const SwaggerUI = `
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Go Rombong API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      SwaggerUIBundle({ url: '/docs/openapi.yaml', dom_id: '#swagger-ui' });
    };
  </script>
</body>
</html>
`
