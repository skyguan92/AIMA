package inferencehttp

// Deps holds dependencies for AIMA's protocol-aware inference HTTP routes.
type Deps struct {
	Backends BackendLister
	Catalog  CatalogReader
}

// BackendLister provides read-only access to the proxy backend table.
type BackendLister interface {
	ListBackends() map[string]*Backend
}

// Backend mirrors proxy.Backend fields needed by the HTTP adapters.
type Backend struct {
	ModelName           string
	EngineType          string
	Address             string
	Ready               bool
	Remote              bool
	ContextWindowTokens int
}

type RequestPatch struct {
	Path           string
	EnginePrefixes []string
	Body           map[string]any
}

type Adapter struct {
	Path string
	Kind string
}

// CatalogReader provides model-specific HTTP adapter hints from the knowledge catalog.
type CatalogReader interface {
	Adapters(name string) []Adapter
	RequestPatches(name string) []RequestPatch
}
