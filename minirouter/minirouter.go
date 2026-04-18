package minirouter

import (
	"net/http"
	"strings"
)

// Params holds path parameters extracted during routing.
type Params map[string]string

// route stores the details of a registered route.
type route struct {
	method  string
	parts   []string
	handler func(http.ResponseWriter, *http.Request, Params)
}

// Router manages the registered routes.
type Router struct {
	routes []route
}

// New creates and returns a new Router instance.
func New() *Router {
	return &Router{routes: make([]route, 0)}
}

// Handle registers a new route with the router.
func (r *Router) Handle(method string, pattern string, handler func(http.ResponseWriter, *http.Request, Params)) {
	parts := strings.Split(pattern, "/")
	r.routes = append(r.routes, route{
		method:  method,
		parts:   parts,
		handler: handler,
	})
}

// Lookup finds a handler and parameters for a given method and path.
func (r *Router) Lookup(method string, path string) (func(http.ResponseWriter, *http.Request, Params), Params, bool) {
	for _, route := range r.routes {
		if route.method == method {
			parts := strings.Split(path, "/")
			if len(route.parts) == len(parts) {
				params := make(Params)
				match := true
				for i, part := range route.parts {
					if strings.HasPrefix(part, ":") {
						paramName := strings.TrimPrefix(part, ":")
						params[paramName] = parts[i]
					} else if part != parts[i] {
						match = false
						break
					}
				}
				if match {
					return route.handler, params, true
				}
			}
		}
	}
	return nil, nil, false
}

// ServeHTTP implements the http.Handler interface for the Router.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	handler, params, ok := r.Lookup(req.Method, req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}
	handler(w, req, params)
}