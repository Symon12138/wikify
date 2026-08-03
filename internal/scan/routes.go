package scan

import (
	"regexp"
	"strings"
)

// Route is an HTTP endpoint registration detected by framework-generic
// patterns (annotations and router-call shapes — no product vocabulary).
type Route struct {
	Method string `json:"method"`         // GET | POST | PUT | DELETE | PATCH | ANY
	Path   string `json:"path"`           // must contain "/"
	File   string `json:"file"`           // repo-relative source file (ToSlash)
	Hint   string `json:"hint,omitempty"` // framework family: spring | jaxrs | go-http | go-router | express | flask
}

const (
	maxRoutesTotal   = 300
	maxRoutesPerFile = 40
)

var (
	// Spring: @GetMapping("/x"), @RequestMapping(value = "/x"), @PostMapping(path = "/x")
	reRouteSpring = regexp.MustCompile(`@(Get|Post|Put|Delete|Patch|Request)Mapping\s*\(\s*(?:(?:value|path)\s*=\s*)?"([^"]+)"`)
	// JAX-RS: @Path("/x") — the verb lives in separate @GET/@POST annotations.
	reRouteJaxRS = regexp.MustCompile(`@Path\s*\(\s*"([^"]+)"\s*\)`)
	// Go net/http: http.HandleFunc("/x", …) / http.Handle("/x", …)
	reRouteGoHTTP = regexp.MustCompile(`http\.Handle(?:Func)?\(\s*"([^"]+)"`)
	// Go routers (gin/echo/chi/mux): r.GET("/x", …), r.Get("/x", …), r.HandleFunc("/x", …)
	reRouteGoCall = regexp.MustCompile(`\.(GET|POST|PUT|DELETE|PATCH|Get|Post|Put|Delete|Patch|Handle|HandleFunc)\(\s*"([^"]+)"`)
	// Express/Fastify: app.get('/x', …), router.post("/x", …)
	reRouteExpress = regexp.MustCompile(`\b(?:app|router|server|fastify)\.(get|post|put|delete|patch|all)\(\s*['"]([^'"]+)['"]`)
	// Flask/FastAPI: @app.route('/x'), @router.get("/x"), @bp.post('/x')
	reRoutePy = regexp.MustCompile(`@\w+\.(route|get|post|put|delete|patch)\(\s*['"]([^'"]+)['"]`)
)

// extractRoutes returns capped route registrations found in text for one file.
// Paths without "/" are dropped to filter template/annotation noise.
func extractRoutes(ext, rel, text string) []Route {
	var out []Route
	seen := map[string]bool{}
	add := func(method, path, hint string) bool {
		path = strings.TrimSpace(path)
		if path == "" || !strings.Contains(path, "/") {
			return len(out) < maxRoutesPerFile
		}
		if method == "" {
			method = "ANY"
		}
		key := method + " " + path
		if !seen[key] {
			seen[key] = true
			out = append(out, Route{Method: method, Path: path, File: rel, Hint: hint})
		}
		return len(out) < maxRoutesPerFile
	}
	switch ext {
	case ".java", ".kt", ".kts", ".scala", ".groovy":
		for _, m := range reRouteSpring.FindAllStringSubmatch(text, maxRoutesPerFile) {
			verb := strings.ToUpper(m[1])
			if verb == "REQUEST" {
				verb = "ANY"
			}
			if !add(verb, m[2], "spring") {
				return out
			}
		}
		for _, m := range reRouteJaxRS.FindAllStringSubmatch(text, maxRoutesPerFile) {
			if !add("ANY", m[1], "jaxrs") {
				return out
			}
		}
	case ".go":
		for _, m := range reRouteGoHTTP.FindAllStringSubmatch(text, maxRoutesPerFile) {
			if !add("ANY", m[1], "go-http") {
				return out
			}
		}
		for _, m := range reRouteGoCall.FindAllStringSubmatch(text, maxRoutesPerFile) {
			verb := strings.ToUpper(m[1])
			if verb == "HANDLE" || verb == "HANDLEFUNC" {
				verb = "ANY"
			}
			if !add(verb, m[2], "go-router") {
				return out
			}
		}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		for _, m := range reRouteExpress.FindAllStringSubmatch(text, maxRoutesPerFile) {
			verb := strings.ToUpper(m[1])
			if verb == "ALL" {
				verb = "ANY"
			}
			if !add(verb, m[2], "express") {
				return out
			}
		}
	case ".py":
		for _, m := range reRoutePy.FindAllStringSubmatch(text, maxRoutesPerFile) {
			verb := strings.ToUpper(m[1])
			if verb == "ROUTE" {
				verb = "ANY"
			}
			if !add(verb, m[2], "flask") {
				return out
			}
		}
	}
	return out
}
